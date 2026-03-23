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
