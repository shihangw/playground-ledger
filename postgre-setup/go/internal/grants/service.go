package grants

import (
	"context"
	"time"

	"github.com/shihangw/playground-ledger/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// Service provides credit grant operations
type Service struct {
	pool    *pgxpool.Pool
	queries *generated.Queries
}

// NewService creates a new grants service
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{
		pool:    pool,
		queries: generated.New(pool),
	}
}

// IssueGrantRequest represents a request to issue a credit grant
type IssueGrantRequest struct {
	AccountID      uuid.UUID
	Amount         decimal.Decimal
	GrantType      string
	ExpiresAt      time.Time
	IdempotencyKey uuid.UUID
	Metadata       []byte
	Priority       int32 // lower = consumed first; default 0
}

// DrawdownResult represents the result of consuming from a single grant
type DrawdownResult struct {
	GrantID  uuid.UUID       `json:"grant_id"`
	Amount   decimal.Decimal `json:"amount"`
	Consumed decimal.Decimal `json:"consumed"`
}

// IssueGrant creates a new credit grant for an account
func (s *Service) IssueGrant(ctx context.Context, req IssueGrantRequest) (*generated.CreditGrant, error) {
	if req.Amount.LessThanOrEqual(decimal.Zero) {
		return nil, ErrInvalidAmount
	}

	// Check idempotency
	existing, err := s.queries.GetGrantByIdempotencyKey(ctx, toPgUUID(req.IdempotencyKey))
	if err == nil {
		return &existing, nil
	}
	if err != pgx.ErrNoRows {
		return nil, err
	}

	// Verify account exists
	_, err = s.queries.GetAccountByID(ctx, toPgUUID(req.AccountID))
	if err != nil {
		return nil, ErrAccountNotFound
	}

	grant, err := s.queries.CreateGrant(ctx, generated.CreateGrantParams{
		AccountID:      toPgUUID(req.AccountID),
		GrantType:      req.GrantType,
		InitialAmount:  req.Amount,
		ExpiresAt:      toPgTimestamptz(req.ExpiresAt),
		IdempotencyKey: toPgUUID(req.IdempotencyKey),
		Metadata:       req.Metadata,
		Priority:       req.Priority,
	})
	if err != nil {
		return nil, err
	}

	return &grant, nil
}

// Drawdown consumes credits from active grants in FIFO order (earliest expiring first)
func (s *Service) Drawdown(ctx context.Context, accountID uuid.UUID, amount decimal.Decimal, idempotencyKey uuid.UUID) ([]DrawdownResult, error) {
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil, ErrInvalidAmount
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)

	// Get active grants ordered by expiration (FIFO)
	activeGrants, err := qtx.GetActiveGrantsByAccount(ctx, toPgUUID(accountID))
	if err != nil {
		return nil, err
	}

	// Calculate total available
	totalAvailable := decimal.Zero
	for _, g := range activeGrants {
		totalAvailable = totalAvailable.Add(g.RemainingAmount)
	}
	if totalAvailable.LessThan(amount) {
		return nil, ErrInsufficientGrants
	}

	remaining := amount
	var results []DrawdownResult

	for _, grant := range activeGrants {
		if remaining.IsZero() {
			break
		}

		// Lock the grant row
		lockedGrant, err := qtx.GetGrantForUpdate(ctx, grant.ID)
		if err != nil {
			return nil, err
		}

		// Skip if no longer active (race condition)
		if lockedGrant.Status != "ACTIVE" {
			continue
		}

		var consumed decimal.Decimal
		if lockedGrant.RemainingAmount.LessThanOrEqual(remaining) {
			// Consume entire grant
			consumed = lockedGrant.RemainingAmount
			if err := qtx.DepleteGrant(ctx, grant.ID); err != nil {
				return nil, err
			}
		} else {
			// Partial drawdown
			consumed = remaining
			_, err := qtx.DrawdownGrant(ctx, generated.DrawdownGrantParams{
				ID:              grant.ID,
				RemainingAmount: consumed,
			})
			if err != nil {
				return nil, err
			}
		}

		newRemaining := lockedGrant.RemainingAmount.Sub(consumed)

		// Create audit entry
		_, err = qtx.CreateGrantLedgerEntry(ctx, generated.CreateGrantLedgerEntryParams{
			GrantID:        grant.ID,
			EntryType:      "DRAWDOWN",
			Amount:         consumed,
			RemainingAfter: newRemaining,
			Description:    toPgText("Credit drawdown"),
			IdempotencyKey: toPgUUID(uuid.New()),
		})
		if err != nil {
			return nil, err
		}

		results = append(results, DrawdownResult{
			GrantID:  fromPgUUID(grant.ID),
			Amount:   consumed,
			Consumed: consumed,
		})

		remaining = remaining.Sub(consumed)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return results, nil
}

// GrantDrawdown is one grant's contribution in a waterfall debit.
type GrantDrawdown struct {
	GrantID  uuid.UUID       `json:"grant_id"`
	Consumed decimal.Decimal `json:"consumed"`
}

// WaterfallResult is the outcome of a WaterfallDebit call.
type WaterfallResult struct {
	GrantsConsumed []GrantDrawdown `json:"grants_consumed"`
	CashConsumed   decimal.Decimal `json:"cash_consumed"`
	NewCashBalance decimal.Decimal `json:"new_cash_balance"`
}

// WaterfallDebit draws amount from active credit grants (FIFO by expiry) and
// falls back to the account's cash balance for any remainder.
//
// Locks acquired in consistent order (account first, then grants by expires_at)
// so concurrent waterfall debits on the same account serialize without deadlocks.
func (s *Service) WaterfallDebit(ctx context.Context, accountID uuid.UUID, amount decimal.Decimal, idempotencyKey uuid.UUID, description string) (*WaterfallResult, error) {
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil, ErrInvalidAmount
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// 1. Lock the account row first (consistent global lock ordering).
	var cashBalance decimal.Decimal
	if err := tx.QueryRow(ctx,
		`SELECT balance FROM accounts WHERE id = $1 FOR UPDATE`,
		toPgUUID(accountID),
	).Scan(&cashBalance); err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrAccountNotFound
		}
		return nil, err
	}

	// 2. Lock all active grants in FIFO order in a single query (consistent ordering).
	rows, err := tx.Query(ctx,
		`SELECT id, remaining_amount FROM credit_grants
		 WHERE account_id = $1 AND status = 'ACTIVE' AND remaining_amount > 0 AND expires_at > now()
		 ORDER BY priority ASC, expires_at ASC
		 FOR UPDATE`,
		toPgUUID(accountID),
	)
	if err != nil {
		return nil, err
	}
	type lockedGrant struct {
		ID              pgtype.UUID
		RemainingAmount decimal.Decimal
	}
	var grants []lockedGrant
	for rows.Next() {
		var g lockedGrant
		if err := rows.Scan(&g.ID, &g.RemainingAmount); err != nil {
			rows.Close()
			return nil, err
		}
		grants = append(grants, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 3. Verify total available funds.
	totalGrantAvailable := decimal.Zero
	for _, g := range grants {
		totalGrantAvailable = totalGrantAvailable.Add(g.RemainingAmount)
	}
	if totalGrantAvailable.Add(cashBalance).LessThan(amount) {
		return nil, ErrInsufficientFunds
	}

	// 4. Consume grants FIFO.
	remaining := amount
	var grantResults []GrantDrawdown
	qtx := s.queries.WithTx(tx)

	for _, grant := range grants {
		if remaining.IsZero() {
			break
		}
		consumed := decimal.Min(grant.RemainingAmount, remaining)
		remaining = remaining.Sub(consumed)
		newRemaining := grant.RemainingAmount.Sub(consumed)

		if newRemaining.IsZero() {
			if err := qtx.DepleteGrant(ctx, grant.ID); err != nil {
				return nil, err
			}
		} else {
			if _, err := qtx.DrawdownGrant(ctx, generated.DrawdownGrantParams{
				ID:              grant.ID,
				RemainingAmount: consumed,
			}); err != nil {
				return nil, err
			}
		}

		if _, err := qtx.CreateGrantLedgerEntry(ctx, generated.CreateGrantLedgerEntryParams{
			GrantID:        grant.ID,
			EntryType:      "DRAWDOWN",
			Amount:         consumed,
			RemainingAfter: newRemaining,
			Description:    toPgText(description),
			IdempotencyKey: toPgUUID(uuid.New()),
		}); err != nil {
			return nil, err
		}

		grantResults = append(grantResults, GrantDrawdown{
			GrantID:  fromPgUUID(grant.ID),
			Consumed: consumed,
		})
	}

	// 5. Deduct any remainder from cash balance (already locked in step 1).
	newCashBalance := cashBalance
	if remaining.IsPositive() {
		newCashBalance = cashBalance.Sub(remaining)
		if _, err := tx.Exec(ctx,
			`UPDATE accounts SET balance = balance - $1, updated_at = now() WHERE id = $2`,
			remaining, toPgUUID(accountID),
		); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO ledger_entries (account_id, entry_type, amount, balance_after, description, metadata)
			 VALUES ($1, 'DEBIT', $2, $3, $4, NULL)`,
			toPgUUID(accountID), remaining, newCashBalance, toPgText(description),
		); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &WaterfallResult{
		GrantsConsumed: grantResults,
		CashConsumed:   remaining,
		NewCashBalance: newCashBalance,
	}, nil
}

// ExpireGrants marks all past-due active grants as expired
func (s *Service) ExpireGrants(ctx context.Context) (int64, error) {
	return s.queries.ExpireActiveGrants(ctx)
}

// GetAvailableBalance returns the total available grant balance for an account
func (s *Service) GetAvailableBalance(ctx context.Context, accountID uuid.UUID) (decimal.Decimal, error) {
	return s.queries.GetGrantBalance(ctx, toPgUUID(accountID))
}

// GetGrants returns grants for an account
func (s *Service) GetGrants(ctx context.Context, accountID uuid.UUID, limit, offset int32) ([]generated.CreditGrant, error) {
	return s.queries.GetGrantsByAccount(ctx, generated.GetGrantsByAccountParams{
		AccountID: toPgUUID(accountID),
		Limit:     limit,
		Offset:    offset,
	})
}

// Helper functions
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

func toPgTimestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
