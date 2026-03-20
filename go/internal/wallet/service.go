package wallet

import (
	"context"

	"github.com/shihangw/playground-ledger/internal/db/generated"
	"github.com/shihangw/playground-ledger/internal/ledger"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// Service provides wallet operations for users
type Service struct {
	pool    *pgxpool.Pool
	queries *generated.Queries
	ledger  *ledger.Service
}

// NewService creates a new wallet service
func NewService(pool *pgxpool.Pool, ledgerService *ledger.Service) *Service {
	return &Service{
		pool:    pool,
		queries: generated.New(pool),
		ledger:  ledgerService,
	}
}

// CreateUserRequest represents a request to create a new user
type CreateUserRequest struct {
	ExternalID string // WorkOS user ID
	Email      string
}

// SignupBonusAmount is the promotional credit for new users
var SignupBonusAmount = decimal.NewFromInt(100)

// CreateUser creates a new user with a default USD account and $100 signup bonus
func (s *Service) CreateUser(ctx context.Context, req CreateUserRequest) (*generated.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)

	// Create user
	user, err := qtx.CreateUser(ctx, generated.CreateUserParams{
		ExternalID: req.ExternalID,
		Email:      req.Email,
	})
	if err != nil {
		return nil, err
	}

	// Create default USD account with $100 signup bonus
	account, err := qtx.CreateAccount(ctx, generated.CreateAccountParams{
		UserID:   user.ID,
		Currency: "USD",
	})
	if err != nil {
		return nil, err
	}

	// Add $100 signup bonus
	idempotencyKey := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("signup_bonus_"+req.ExternalID))
	_, err = qtx.CreditAccount(ctx, generated.CreditAccountParams{
		ID:      account.ID,
		Balance: SignupBonusAmount,
	})
	if err != nil {
		return nil, err
	}

	// Create transaction record FIRST (ledger entry references it)
	txn, err := qtx.CreateTransaction(ctx, generated.CreateTransactionParams{
		TransactionType:      "DEPOSIT",
		Status:               "COMPLETED",
		DestinationAccountID: account.ID,
		Amount:               SignupBonusAmount,
		Currency:             "USD",
		IdempotencyKey:       toPgUUID(idempotencyKey),
	})
	if err != nil {
		return nil, err
	}

	// Create ledger entry for the signup bonus (references the transaction)
	_, err = qtx.CreateLedgerEntry(ctx, generated.CreateLedgerEntryParams{
		AccountID:      account.ID,
		EntryType:      "CREDIT",
		Amount:         SignupBonusAmount,
		BalanceAfter:   SignupBonusAmount,
		TransactionID:  txn.ID,
		Description:    toPgText("Welcome bonus - $100 signup credit"),
		IdempotencyKey: toPgUUID(idempotencyKey),
	})
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &user, nil
}

// GetOrCreateUser gets a user by external ID or creates them
func (s *Service) GetOrCreateUser(ctx context.Context, externalID, email string) (*generated.User, error) {
	user, err := s.queries.GetUserByExternalID(ctx, externalID)
	if err == nil {
		return &user, nil
	}
	if err != pgx.ErrNoRows {
		return nil, err
	}

	// Create new user
	return s.CreateUser(ctx, CreateUserRequest{
		ExternalID: externalID,
		Email:      email,
	})
}

// GetUserByExternalID returns a user by their external (WorkOS) ID
func (s *Service) GetUserByExternalID(ctx context.Context, externalID string) (*generated.User, error) {
	user, err := s.queries.GetUserByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// GetOrCreateAccount gets or creates an account for a user in a specific currency
func (s *Service) GetOrCreateAccount(ctx context.Context, userID uuid.UUID, currency string) (*generated.Account, error) {
	account, err := s.queries.GetOrCreateAccount(ctx, generated.GetOrCreateAccountParams{
		UserID:   toPgUUID(userID),
		Currency: currency,
	})
	if err != nil {
		return nil, err
	}
	return &account, nil
}

// GetAccountsByUser returns all accounts for a user
func (s *Service) GetAccountsByUser(ctx context.Context, userID uuid.UUID) ([]generated.Account, error) {
	return s.queries.GetAccountsByUser(ctx, toPgUUID(userID))
}

// GetBalance returns the balance for a specific account
func (s *Service) GetBalance(ctx context.Context, accountID uuid.UUID) (decimal.Decimal, error) {
	return s.ledger.GetBalance(ctx, accountID)
}

// Deposit adds funds to an account
func (s *Service) Deposit(ctx context.Context, accountID uuid.UUID, amount decimal.Decimal, idempotencyKey uuid.UUID, description string) (*generated.Transaction, error) {
	return s.ledger.Deposit(ctx, ledger.DepositRequest{
		AccountID:      accountID,
		Amount:         amount,
		Description:    description,
		IdempotencyKey: idempotencyKey,
	})
}

// Withdraw removes funds from an account
func (s *Service) Withdraw(ctx context.Context, accountID uuid.UUID, amount decimal.Decimal, idempotencyKey uuid.UUID, description string) (*generated.Transaction, error) {
	return s.ledger.Withdraw(ctx, ledger.WithdrawRequest{
		AccountID:      accountID,
		Amount:         amount,
		Description:    description,
		IdempotencyKey: idempotencyKey,
	})
}

// Transfer moves funds between accounts
func (s *Service) Transfer(ctx context.Context, fromAccountID, toAccountID uuid.UUID, amount decimal.Decimal, idempotencyKey uuid.UUID, description string) (*generated.Transaction, error) {
	return s.ledger.Transfer(ctx, ledger.TransferRequest{
		SourceAccountID:      fromAccountID,
		DestinationAccountID: toAccountID,
		Amount:               amount,
		Description:          description,
		IdempotencyKey:       idempotencyKey,
	})
}

// GetTransactions returns transactions for an account
func (s *Service) GetTransactions(ctx context.Context, accountID uuid.UUID, limit, offset int32) ([]generated.Transaction, error) {
	return s.ledger.GetAccountTransactions(ctx, accountID, limit, offset)
}

// GetLedgerEntries returns ledger entries for an account
func (s *Service) GetLedgerEntries(ctx context.Context, accountID uuid.UUID, limit, offset int32) ([]generated.LedgerEntry, error) {
	return s.ledger.GetLedgerEntries(ctx, accountID, limit, offset)
}

// GetAllAccounts returns all accounts in the system (admin only)
func (s *Service) GetAllAccounts(ctx context.Context, limit, offset int32) ([]generated.GetAllAccountsRow, error) {
	return s.queries.GetAllAccounts(ctx, generated.GetAllAccountsParams{
		Limit:  limit,
		Offset: offset,
	})
}

// Helper function to convert uuid.UUID to pgtype.UUID
func toPgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

// Helper function to convert string to pgtype.Text
func toPgText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: true}
}
