package ledger

import (
	"context"

	"github.com/shihangw/playground-ledger/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// Service provides ledger operations
type Service struct {
	pool    *pgxpool.Pool
	queries *generated.Queries
}

// NewService creates a new ledger service
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{
		pool:    pool,
		queries: generated.New(pool),
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
	// Validate
	if req.Amount.LessThanOrEqual(decimal.Zero) {
		return nil, ErrInvalidAmount
	}
	if req.SourceAccountID == req.DestinationAccountID {
		return nil, ErrSameAccount
	}

	// Check for existing transaction (idempotency)
	existing, err := s.queries.GetTransactionByIdempotencyKey(ctx, toPgUUID(req.IdempotencyKey))
	if err == nil {
		return &existing, nil
	}
	if err != pgx.ErrNoRows {
		return nil, err
	}

	// Execute in transaction
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)

	// Lock accounts in consistent order to prevent deadlocks
	var sourceAccount, destAccount generated.Account
	if req.SourceAccountID.String() < req.DestinationAccountID.String() {
		sourceAccount, err = qtx.GetAccountForUpdate(ctx, toPgUUID(req.SourceAccountID))
		if err != nil {
			return nil, ErrAccountNotFound
		}
		destAccount, err = qtx.GetAccountForUpdate(ctx, toPgUUID(req.DestinationAccountID))
		if err != nil {
			return nil, ErrAccountNotFound
		}
	} else {
		destAccount, err = qtx.GetAccountForUpdate(ctx, toPgUUID(req.DestinationAccountID))
		if err != nil {
			return nil, ErrAccountNotFound
		}
		sourceAccount, err = qtx.GetAccountForUpdate(ctx, toPgUUID(req.SourceAccountID))
		if err != nil {
			return nil, ErrAccountNotFound
		}
	}

	// Check balance
	if sourceAccount.Balance.LessThan(req.Amount) {
		return nil, ErrInsufficientFunds
	}

	// Check currency match
	if sourceAccount.Currency != destAccount.Currency {
		return nil, ErrCurrencyMismatch
	}

	// Calculate new balances
	newSourceBalance := sourceAccount.Balance.Sub(req.Amount)
	newDestBalance := destAccount.Balance.Add(req.Amount)

	// Update source account
	_, err = qtx.DebitAccount(ctx, generated.DebitAccountParams{
		ID:      sourceAccount.ID,
		Balance: req.Amount,
	})
	if err != nil {
		return nil, err
	}

	// Update destination account
	_, err = qtx.CreditAccount(ctx, generated.CreditAccountParams{
		ID:      destAccount.ID,
		Balance: req.Amount,
	})
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

	// Create debit ledger entry
	_, err = qtx.CreateLedgerEntry(ctx, generated.CreateLedgerEntryParams{
		AccountID:      sourceAccount.ID,
		EntryType:      "DEBIT",
		Amount:         req.Amount,
		BalanceAfter:   newSourceBalance,
		TransactionID:  txn.ID,
		Description:    toPgText(req.Description),
		Metadata:       req.Metadata,
		IdempotencyKey: toPgUUID(uuid.New()),
	})
	if err != nil {
		return nil, err
	}

	// Create credit ledger entry
	_, err = qtx.CreateLedgerEntry(ctx, generated.CreateLedgerEntryParams{
		AccountID:      destAccount.ID,
		EntryType:      "CREDIT",
		Amount:         req.Amount,
		BalanceAfter:   newDestBalance,
		TransactionID:  txn.ID,
		Description:    toPgText(req.Description),
		Metadata:       req.Metadata,
		IdempotencyKey: toPgUUID(uuid.New()),
	})
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &txn, nil
}

// Deposit adds funds to an account
func (s *Service) Deposit(ctx context.Context, req DepositRequest) (*generated.Transaction, error) {
	if req.Amount.LessThanOrEqual(decimal.Zero) {
		return nil, ErrInvalidAmount
	}

	// Check for existing transaction
	existing, err := s.queries.GetTransactionByIdempotencyKey(ctx, toPgUUID(req.IdempotencyKey))
	if err == nil {
		return &existing, nil
	}
	if err != pgx.ErrNoRows {
		return nil, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)

	// Lock account
	account, err := qtx.GetAccountForUpdate(ctx, toPgUUID(req.AccountID))
	if err != nil {
		return nil, ErrAccountNotFound
	}

	newBalance := account.Balance.Add(req.Amount)

	// Credit account
	_, err = qtx.CreditAccount(ctx, generated.CreditAccountParams{
		ID:      account.ID,
		Balance: req.Amount,
	})
	if err != nil {
		return nil, err
	}

	// Create transaction
	txn, err := qtx.CreateTransaction(ctx, generated.CreateTransactionParams{
		TransactionType:      "DEPOSIT",
		Status:               "COMPLETED",
		DestinationAccountID: toPgUUID(req.AccountID),
		Amount:               req.Amount,
		Currency:             account.Currency,
		IdempotencyKey:       toPgUUID(req.IdempotencyKey),
		Metadata:             req.Metadata,
	})
	if err != nil {
		return nil, err
	}

	// Create ledger entry
	_, err = qtx.CreateLedgerEntry(ctx, generated.CreateLedgerEntryParams{
		AccountID:      account.ID,
		EntryType:      "CREDIT",
		Amount:         req.Amount,
		BalanceAfter:   newBalance,
		TransactionID:  txn.ID,
		Description:    toPgText(req.Description),
		Metadata:       req.Metadata,
		IdempotencyKey: toPgUUID(uuid.New()),
	})
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &txn, nil
}

// Withdraw removes funds from an account
func (s *Service) Withdraw(ctx context.Context, req WithdrawRequest) (*generated.Transaction, error) {
	if req.Amount.LessThanOrEqual(decimal.Zero) {
		return nil, ErrInvalidAmount
	}

	// Check for existing transaction
	existing, err := s.queries.GetTransactionByIdempotencyKey(ctx, toPgUUID(req.IdempotencyKey))
	if err == nil {
		return &existing, nil
	}
	if err != pgx.ErrNoRows {
		return nil, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)

	// Lock account
	account, err := qtx.GetAccountForUpdate(ctx, toPgUUID(req.AccountID))
	if err != nil {
		return nil, ErrAccountNotFound
	}

	if account.Balance.LessThan(req.Amount) {
		return nil, ErrInsufficientFunds
	}

	newBalance := account.Balance.Sub(req.Amount)

	// Debit account
	_, err = qtx.DebitAccount(ctx, generated.DebitAccountParams{
		ID:      account.ID,
		Balance: req.Amount,
	})
	if err != nil {
		return nil, err
	}

	// Create transaction
	txn, err := qtx.CreateTransaction(ctx, generated.CreateTransactionParams{
		TransactionType: "WITHDRAWAL",
		Status:          "COMPLETED",
		SourceAccountID: toPgUUID(req.AccountID),
		Amount:          req.Amount,
		Currency:        account.Currency,
		IdempotencyKey:  toPgUUID(req.IdempotencyKey),
		Metadata:        req.Metadata,
	})
	if err != nil {
		return nil, err
	}

	// Create ledger entry
	_, err = qtx.CreateLedgerEntry(ctx, generated.CreateLedgerEntryParams{
		AccountID:      account.ID,
		EntryType:      "DEBIT",
		Amount:         req.Amount,
		BalanceAfter:   newBalance,
		TransactionID:  txn.ID,
		Description:    toPgText(req.Description),
		Metadata:       req.Metadata,
		IdempotencyKey: toPgUUID(uuid.New()),
	})
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
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
