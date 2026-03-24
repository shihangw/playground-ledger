package ledger

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/shihangw/playground-ledger/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// histBuckets are upper bounds in ms: <5, <15, <30, <60, <120, >=120
var histBuckets = [...]int64{5, 15, 30, 60, 120}

type opStats struct {
	count      atomic.Int64
	idempotent atomic.Int64
	totalMs    atomic.Int64
	idpMs      atomic.Int64 // idempotency check
	lockMs     atomic.Int64 // acquire row lock
	writesMs   atomic.Int64 // db writes
	commitMs   atomic.Int64 // tx commit
	hist       [6]atomic.Int64
}

type phases struct {
	idpMs, lockMs, writesMs, commitMs int64
}

func (s *opStats) record(totalMs int64, p phases) {
	s.count.Add(1)
	s.totalMs.Add(totalMs)
	s.idpMs.Add(p.idpMs)
	s.lockMs.Add(p.lockMs)
	s.writesMs.Add(p.writesMs)
	s.commitMs.Add(p.commitMs)
	bucket := len(histBuckets) // last bucket = slow
	for i, b := range histBuckets {
		if totalMs < b {
			bucket = i
			break
		}
	}
	s.hist[bucket].Add(1)
}

func (s *opStats) recordIdempotent(totalMs int64) {
	s.idempotent.Add(1)
}

func (s *opStats) snapshot() (count, idempotent, total, idp, lock, writes, commit int64, hist [6]int64) {
	count = s.count.Swap(0)
	idempotent = s.idempotent.Swap(0)
	total = s.totalMs.Swap(0)
	idp = s.idpMs.Swap(0)
	lock = s.lockMs.Swap(0)
	writes = s.writesMs.Swap(0)
	commit = s.commitMs.Swap(0)
	for i := range hist {
		hist[i] = s.hist[i].Swap(0)
	}
	return
}

// Service provides ledger operations
type Service struct {
	pool    *pgxpool.Pool
	queries *generated.Queries

	inFlight    atomic.Int64
	deposits    opStats
	withdrawals opStats
	transfers   opStats
}

// NewService creates a new ledger service
func NewService(pool *pgxpool.Pool) *Service {
	s := &Service{
		pool:    pool,
		queries: generated.New(pool),
	}
	go s.logStats()
	return s
}

func (s *Service) logStats() {
	avg := func(count, totalMs int64) string {
		if count == 0 {
			return "-"
		}
		return fmt.Sprintf("%dms", totalMs/count)
	}
	phaseLine := func(name string, count, total, idp, lock, writes, commit int64, hist [6]int64) string {
		if count == 0 {
			return ""
		}
		pct := func(n int64) string {
			if count == 0 {
				return "0%"
			}
			return fmt.Sprintf("%d%%", n*100/count)
		}
		slow := hist[5]
		return fmt.Sprintf("%s=%-4d avg=%-5s phases[idp=%-4s lock=%-4s write=%-4s commit=%-4s]  hist[<5ms=%s <15ms=%s <30ms=%s <60ms=%s <120ms=%s slow=%s]",
			name, count,
			avg(count, total),
			avg(count, idp), avg(count, lock), avg(count, writes), avg(count, commit),
			pct(hist[0]), pct(hist[1]), pct(hist[2]), pct(hist[3]), pct(hist[4]), pct(slow),
		)
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		dc, di, dt, didp, dlock, dwrites, dcommit, dhist := s.deposits.snapshot()
		wc, wi, wt, widp, wlock, wwrites, wcommit, whist := s.withdrawals.snapshot()
		tc, ti, tt, tidp, tlock, twrites, tcommit, thist := s.transfers.snapshot()

		if dc+wc+tc+di+wi+ti == 0 {
			continue
		}

		dp := phaseLine("deposits", dc, dt, didp, dlock, dwrites, dcommit, dhist)
		wp := phaseLine("withdrawals", wc, wt, widp, wlock, wwrites, wcommit, whist)
		tp := phaseLine("transfers", tc, tt, tidp, tlock, twrites, tcommit, thist)

		log.Printf("[ledger/10s] in_flight=%d  idempotent=%d", s.inFlight.Load(), di+wi+ti)
		if dp != "" {
			log.Printf("  %s", dp)
		}
		if wp != "" {
			log.Printf("  %s", wp)
		}
		if tp != "" {
			log.Printf("  %s", tp)
		}
	}
}

// TransferRequest represents a transfer between accounts
type TransferRequest struct {
	SourceAccountID      uuid.UUID
	DestinationAccountID uuid.UUID
	Amount               decimal.Decimal
	Description          string
	IdempotencyKey       uuid.UUID
	Metadata             []byte // JSON
}

// DepositRequest represents a deposit to an account
type DepositRequest struct {
	AccountID      uuid.UUID
	Amount         decimal.Decimal
	Description    string
	IdempotencyKey uuid.UUID
	Metadata       []byte
}

// WithdrawRequest represents a withdrawal from an account
type WithdrawRequest struct {
	AccountID      uuid.UUID
	Amount         decimal.Decimal
	Description    string
	IdempotencyKey uuid.UUID
	Metadata       []byte
}

// Transfer executes an atomic transfer between two accounts
func (s *Service) Transfer(ctx context.Context, req TransferRequest) (*generated.Transaction, error) {
	t0 := time.Now()
	s.inFlight.Add(1)
	defer s.inFlight.Add(-1)

	// Validate
	if req.Amount.LessThanOrEqual(decimal.Zero) {
		return nil, ErrInvalidAmount
	}
	if req.SourceAccountID == req.DestinationAccountID {
		return nil, ErrSameAccount
	}

	// Acquire one connection for the entire operation (idempotency check + transaction)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)

	// Check for existing transaction (idempotency) inside the transaction
	tIdp := time.Now()
	existing, err := qtx.GetTransactionByIdempotencyKey(ctx, toPgUUID(req.IdempotencyKey))
	if err == nil {
		s.transfers.recordIdempotent(ms(t0))
		return &existing, nil
	}
	if err != pgx.ErrNoRows {
		return nil, err
	}
	dIdp := ms(tIdp)

	// Read both accounts (non-locking) for currency validation and balance pre-check.
	// The actual atomic balance check is enforced by DebitAccount's WHERE balance >= $2.
	tLock := time.Now()
	sourceAccount, err := qtx.GetAccountByID(ctx, toPgUUID(req.SourceAccountID))
	if err != nil {
		return nil, ErrAccountNotFound
	}
	destAccount, err := qtx.GetAccountByID(ctx, toPgUUID(req.DestinationAccountID))
	if err != nil {
		return nil, ErrAccountNotFound
	}
	dLock := ms(tLock)

	if sourceAccount.Currency != destAccount.Currency {
		return nil, ErrCurrencyMismatch
	}
	if sourceAccount.Balance.LessThan(req.Amount) {
		return nil, ErrInsufficientFunds
	}

	// Apply updates in consistent ID order to prevent deadlocks.
	// DebitAccount atomically enforces balance >= amount.
	tWrites := time.Now()
	var updatedSource generated.Account
	if req.SourceAccountID.String() < req.DestinationAccountID.String() {
		updatedSource, err = qtx.DebitAccount(ctx, generated.DebitAccountParams{ID: toPgUUID(req.SourceAccountID), Balance: req.Amount})
		if err != nil {
			if err == pgx.ErrNoRows {
				return nil, ErrInsufficientFunds
			}
			return nil, err
		}
		_, err = qtx.CreditAccount(ctx, generated.CreditAccountParams{ID: toPgUUID(req.DestinationAccountID), Balance: req.Amount})
	} else {
		_, err = qtx.CreditAccount(ctx, generated.CreditAccountParams{ID: toPgUUID(req.DestinationAccountID), Balance: req.Amount})
		if err != nil {
			return nil, err
		}
		updatedSource, err = qtx.DebitAccount(ctx, generated.DebitAccountParams{ID: toPgUUID(req.SourceAccountID), Balance: req.Amount})
		if err != nil {
			if err == pgx.ErrNoRows {
				return nil, ErrInsufficientFunds
			}
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}

	updatedDest, err := qtx.GetAccountByID(ctx, toPgUUID(req.DestinationAccountID))
	if err != nil {
		return nil, err
	}

	// Create transaction record
	txn, err := qtx.CreateTransaction(ctx, generated.CreateTransactionParams{
		TransactionType:      "TRANSFER",
		Status:               "COMPLETED",
		SourceAccountID:      toPgUUID(req.SourceAccountID),
		DestinationAccountID: toPgUUID(req.DestinationAccountID),
		Amount:               req.Amount,
		Currency:             sourceAccount.Currency,
		IdempotencyKey:       toPgUUID(req.IdempotencyKey),
		Metadata:             req.Metadata,
	})
	if err != nil {
		return nil, err
	}

	_, err = qtx.CreateLedgerEntry(ctx, generated.CreateLedgerEntryParams{
		AccountID:      toPgUUID(req.SourceAccountID),
		EntryType:      "DEBIT",
		Amount:         req.Amount,
		BalanceAfter:   updatedSource.Balance,
		TransactionID:  txn.ID,
		Description:    toPgText(req.Description),
		Metadata:       req.Metadata,
		IdempotencyKey: toPgUUID(uuid.New()),
	})
	if err != nil {
		return nil, err
	}

	_, err = qtx.CreateLedgerEntry(ctx, generated.CreateLedgerEntryParams{
		AccountID:      toPgUUID(req.DestinationAccountID),
		EntryType:      "CREDIT",
		Amount:         req.Amount,
		BalanceAfter:   updatedDest.Balance,
		TransactionID:  txn.ID,
		Description:    toPgText(req.Description),
		Metadata:       req.Metadata,
		IdempotencyKey: toPgUUID(uuid.New()),
	})
	if err != nil {
		return nil, err
	}
	dWrites := ms(tWrites)

	tCommit := time.Now()
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	dCommit := ms(tCommit)

	s.transfers.record(ms(t0), phases{dIdp, dLock, dWrites, dCommit})
	return &txn, nil
}

// Deposit adds funds to an account
func (s *Service) Deposit(ctx context.Context, req DepositRequest) (*generated.Transaction, error) {
	t0 := time.Now()
	s.inFlight.Add(1)
	defer s.inFlight.Add(-1)

	if req.Amount.LessThanOrEqual(decimal.Zero) {
		return nil, ErrInvalidAmount
	}

	tWrites := time.Now()
	row, err := s.queries.DepositAtomic(ctx, generated.DepositAtomicParams{
		Amount:      req.Amount,
		AccountID:   toPgUUID(req.AccountID),
		Description: toPgText(req.Description),
		Metadata:    req.Metadata,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrAccountNotFound
		}
		if isContention(err) {
			return nil, ErrContention
		}
		return nil, err
	}
	dWrites := ms(tWrites)

	s.deposits.record(ms(t0), phases{0, 0, dWrites, 0})
	txn := generated.Transaction{
		ID: toPgUUID(uuid.New()), TransactionType: "DEPOSIT", Status: "COMPLETED",
		DestinationAccountID: toPgUUID(req.AccountID),
		Amount: req.Amount, Currency: row.Currency,
		IdempotencyKey: toPgUUID(req.IdempotencyKey),
		Metadata: req.Metadata, CreatedAt: row.CreatedAt,
	}
	return &txn, nil
}

// Withdraw removes funds from an account
func (s *Service) Withdraw(ctx context.Context, req WithdrawRequest) (*generated.Transaction, error) {
	t0 := time.Now()
	s.inFlight.Add(1)
	defer s.inFlight.Add(-1)

	if req.Amount.LessThanOrEqual(decimal.Zero) {
		return nil, ErrInvalidAmount
	}

	tWrites := time.Now()
	row, err := s.queries.WithdrawAtomic(ctx, generated.WithdrawAtomicParams{
		Amount:      req.Amount,
		AccountID:   toPgUUID(req.AccountID),
		Description: toPgText(req.Description),
		Metadata:    req.Metadata,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrAccountNotFound
		}
		if isContention(err) {
			return nil, ErrContention
		}
		return nil, err
	}
	dWrites := ms(tWrites)

	s.withdrawals.record(ms(t0), phases{0, 0, dWrites, 0})
	txn := generated.Transaction{
		ID: toPgUUID(uuid.New()), TransactionType: "WITHDRAWAL", Status: "COMPLETED",
		SourceAccountID: toPgUUID(req.AccountID),
		Amount: req.Amount, Currency: row.Currency,
		IdempotencyKey: toPgUUID(req.IdempotencyKey),
		Metadata: req.Metadata, CreatedAt: row.CreatedAt,
	}
	return &txn, nil
}

// GetBalance returns the current balance of an account
func (s *Service) GetBalance(ctx context.Context, accountID uuid.UUID) (decimal.Decimal, error) {
	account, err := s.queries.GetAccountByID(ctx, toPgUUID(accountID))
	if err != nil {
		return decimal.Zero, ErrAccountNotFound
	}
	return account.Balance, nil
}

// GetAccountTransactions returns transactions for an account
func (s *Service) GetAccountTransactions(ctx context.Context, accountID uuid.UUID, limit, offset int32) ([]generated.Transaction, error) {
	return s.queries.GetTransactionsByAccount(ctx, generated.GetTransactionsByAccountParams{
		SourceAccountID: toPgUUID(accountID),
		Limit:           limit,
		Offset:          offset,
	})
}

// GetLedgerEntries returns ledger entries for an account
func (s *Service) GetLedgerEntries(ctx context.Context, accountID uuid.UUID, limit, offset int32) ([]generated.LedgerEntry, error) {
	return s.queries.GetLedgerEntriesByAccount(ctx, generated.GetLedgerEntriesByAccountParams{
		AccountID: toPgUUID(accountID),
		Limit:     limit,
		Offset:    offset,
	})
}

// BatchOpType identifies the type of a batch operation.
type BatchOpType string

const (
	BatchOpDeposit  BatchOpType = "deposit"
	BatchOpWithdraw BatchOpType = "withdraw"
)

// BatchOp is one operation in a batch execution.
type BatchOp struct {
	Type      BatchOpType
	AccountID uuid.UUID
	Amount    decimal.Decimal
}

// BatchResult is the outcome of one batch operation.
type BatchResult struct {
	Success bool
	Err     error
}

const depositAtomicSQL = `
WITH credit AS (
  UPDATE accounts SET balance = balance + $1::numeric, updated_at = now()
  WHERE accounts.id = $2
  RETURNING accounts.id, accounts.balance, accounts.currency
), entry AS (
  INSERT INTO ledger_entries (account_id, entry_type, amount, balance_after, description, metadata)
  SELECT credit.id, 'CREDIT', $1::numeric, credit.balance, $3, NULL
  FROM credit
  RETURNING ledger_entries.created_at
)
SELECT credit.currency, entry.created_at FROM credit, entry`

const withdrawAtomicSQL = `
WITH debit AS (
  UPDATE accounts SET balance = balance - $1::numeric, updated_at = now()
  WHERE accounts.id = $2
  RETURNING accounts.id, accounts.balance, accounts.currency
), entry AS (
  INSERT INTO ledger_entries (account_id, entry_type, amount, balance_after, description, metadata)
  SELECT debit.id, 'DEBIT', $1::numeric, debit.balance, $3, NULL
  FROM debit
  RETURNING ledger_entries.created_at
)
SELECT debit.currency, entry.created_at FROM debit, entry`

// batchSyncGroup is how many ops share one PostgreSQL pipeline Sync boundary.
// A Sync isolates error recovery: when op[i] fails with a lock-timeout,
// PostgreSQL skips ops[i+1..group_end] in the same Sync group (pipeline error
// state), then resets at the next Sync so subsequent groups run normally.
//
// pgx.Batch uses a single Sync for the whole batch, so one failure poisons all
// remaining results in Go (pgx.pipelineBatchResults.err propagation). With
// group_size=10, at most 10 ops are mislabeled per failure instead of N.
//
// Network cost: (N/10) ReadyForQuery messages instead of 1, all sent in a
// single Flush() — still one TCP round-trip.
const batchSyncGroup = 10

// ExecBatch executes multiple deposit/withdraw operations pipelined over a
// single pgconn.Pipeline connection. Ops are grouped into blocks of
// batchSyncGroup, each with its own Sync. All queries are sent in one Flush
// (one network round-trip). This limits error-poisoning to at most
// batchSyncGroup ops per failure rather than the entire batch.
func (s *Service) ExecBatch(ctx context.Context, ops []BatchOp) []BatchResult {
	results := make([]BatchResult, len(ops))
	if len(ops) == 0 {
		return results
	}

	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		for i := range results {
			results[i] = BatchResult{Err: err}
		}
		return results
	}
	defer conn.Release()

	pgConn := conn.Conn().PgConn()
	pipeline := pgConn.StartPipeline(ctx)

	// Enqueue all ops; insert a Sync at the end of every batchSyncGroup block.
	for i, op := range ops {
		sql := depositAtomicSQL
		if op.Type != BatchOpDeposit {
			sql = withdrawAtomicSQL
		}
		pipeline.SendQueryParams(sql,
			[][]byte{[]byte(op.Amount.String()), []byte(op.AccountID.String()), {}},
			[]uint32{0, 0, 0},
			[]int16{0, 0, 0},
			[]int16{0, 0},
		)
		if (i+1)%batchSyncGroup == 0 || i == len(ops)-1 {
			pipeline.SendPipelineSync()
		}
	}
	if err := pipeline.Flush(); err != nil {
		for i := range results {
			results[i] = BatchResult{Err: err}
		}
		return results
	}

	// Read results. GetResults() returns one of:
	//   (*pgconn.ResultReader, nil) — query produced a result set
	//   (*pgconn.PipelineSync,  nil) — ReadyForQuery from a SendPipelineSync
	//   (nil, pgErr)               — query failed (ErrorResponse from server)
	//   (nil, nil)                 — no more results
	// ResultReaders and errors advance the op index; PipelineSyncs do not.
	opIdx := 0
	for opIdx < len(ops) {
		raw, qErr := pipeline.GetResults()
		if raw == nil && qErr == nil {
			break // no more results
		}
		if qErr != nil {
			// query failed (lock-timeout, aborted pipeline, etc.)
			results[opIdx] = BatchResult{Err: qErr}
			opIdx++
			continue
		}
		switch r := raw.(type) {
		case *pgconn.ResultReader:
			if r.NextRow() {
				results[opIdx] = BatchResult{Success: true}
				_, _ = r.Close()
			} else {
				_, closeErr := r.Close()
				if closeErr != nil {
					results[opIdx] = BatchResult{Err: closeErr}
				} else {
					results[opIdx] = BatchResult{Err: pgx.ErrNoRows}
				}
			}
			opIdx++
		case *pgconn.PipelineSync:
			// ReadyForQuery received — pipeline error state cleared, don't advance opIdx
		}
	}
	// Drain any remaining PipelineSyncs so the connection is clean.
	for {
		raw, err := pipeline.GetResults()
		if raw == nil && err == nil {
			break
		}
	}
	return results
}

// isContention returns true for PostgreSQL errors that indicate write contention:
// deadlock (40P01) or lock timeout (55P03).
func isContention(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "40P01" || pgErr.Code == "55P03"
	}
	return false
}

// ms returns milliseconds elapsed since t.
func ms(t time.Time) int64 {
	return time.Since(t).Milliseconds()
}

// Helper functions to convert between uuid.UUID and pgtype.UUID
func toPgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func fromPgUUID(p pgtype.UUID) uuid.UUID {
	if !p.Valid {
		return uuid.Nil
	}
	return p.Bytes
}

func toPgText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: s, Valid: true}
}
