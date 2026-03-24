package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/shihangw/playground-ledger/internal/api/middleware"
	"github.com/shihangw/playground-ledger/internal/db/generated"
	"github.com/shihangw/playground-ledger/internal/ledger"
	"github.com/shihangw/playground-ledger/internal/wallet"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

// WalletHandler handles wallet-related HTTP requests
type WalletHandler struct {
	walletService *wallet.Service
}

// NewWalletHandler creates a new wallet handler.
func NewWalletHandler(walletService *wallet.Service, _ int) *WalletHandler {
	return &WalletHandler{
		walletService: walletService,
	}
}

// DepositRequest represents a deposit request body
type DepositRequest struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// WithdrawRequest represents a withdrawal request body
type WithdrawRequest struct {
	Amount string `json:"amount"`
}

// TransferRequest represents a transfer request body
type TransferRequest struct {
	ToAccountID string `json:"to_account_id"`
	Amount      string `json:"amount"`
}

// AccountResponse represents an account in API responses
type AccountResponse struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Currency  string `json:"currency"`
	Balance   string `json:"balance"`
	CreatedAt string `json:"created_at"`
}

// TransactionResponse represents a transaction in API responses
type TransactionResponse struct {
	ID                   string  `json:"id"`
	Type                 string  `json:"type"`
	Status               string  `json:"status"`
	SourceAccountID      *string `json:"source_account_id,omitempty"`
	DestinationAccountID *string `json:"destination_account_id,omitempty"`
	Amount               string  `json:"amount"`
	Currency             string  `json:"currency"`
	CreatedAt            string  `json:"created_at"`
}

// GetAccounts returns all accounts for the authenticated user
func (h *WalletHandler) GetAccounts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := middleware.GetUserID(ctx)
	email := middleware.GetUserEmail(ctx)

	// Get or create user
	user, err := h.walletService.GetOrCreateUser(ctx, userID, email)
	if err != nil {
		log.Printf("Failed to get/create user (externalID=%s, email=%s): %v", userID, email, err)
		writeError(w, http.StatusInternalServerError, "failed to get user")
		return
	}

	accounts, err := h.walletService.GetAccountsByUser(ctx, fromPgUUID(user.ID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get accounts")
		return
	}

	response := make([]AccountResponse, len(accounts))
	for i, acc := range accounts {
		response[i] = AccountResponse{
			ID:        uuidToString(acc.ID),
			UserID:    uuidToString(acc.UserID),
			Currency:  acc.Currency,
			Balance:   acc.Balance.String(),
			CreatedAt: timestampToString(acc.CreatedAt),
		}
	}

	writeJSON(w, http.StatusOK, response)
}

// GetAccount returns a specific account
func (h *WalletHandler) GetAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountIDStr := r.PathValue("account_id")

	accountID, err := uuid.Parse(accountIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account_id")
		return
	}

	balance, err := h.walletService.GetBalance(ctx, accountID)
	if err != nil {
		if err == ledger.ErrAccountNotFound {
			writeError(w, http.StatusNotFound, "account not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get balance")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"account_id": accountID.String(),
		"balance":    balance.String(),
	})
}

// Deposit handles deposit requests
func (h *WalletHandler) Deposit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountIDStr := r.PathValue("account_id")

	accountID, err := uuid.Parse(accountIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account_id")
		return
	}

	var req DepositRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid amount")
		return
	}

	idempotencyKey, ok := middleware.GetIdempotencyKey(ctx)
	if !ok {
		writeError(w, http.StatusBadRequest, "missing idempotency key")
		return
	}

	txn, err := h.walletService.Deposit(ctx, accountID, amount, idempotencyKey, "Deposit")
	if err != nil {
		if err == ledger.ErrAccountNotFound {
			writeError(w, http.StatusNotFound, "account not found")
			return
		}
		if err == ledger.ErrInvalidAmount {
			writeError(w, http.StatusBadRequest, "invalid amount")
			return
		}
		if err == ledger.ErrContention {
			writeError(w, http.StatusConflict, "contention")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to process deposit")
		return
	}

	writeJSON(w, http.StatusOK, transactionToResponse(txn))
}

// Withdraw handles withdrawal requests
func (h *WalletHandler) Withdraw(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountIDStr := r.PathValue("account_id")

	accountID, err := uuid.Parse(accountIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account_id")
		return
	}

	var req WithdrawRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid amount")
		return
	}

	idempotencyKey, ok := middleware.GetIdempotencyKey(ctx)
	if !ok {
		writeError(w, http.StatusBadRequest, "missing idempotency key")
		return
	}

	txn, err := h.walletService.Withdraw(ctx, accountID, amount, idempotencyKey, "Withdrawal")
	if err != nil {
		if err == ledger.ErrAccountNotFound {
			writeError(w, http.StatusNotFound, "account not found")
			return
		}
		if err == ledger.ErrInsufficientFunds {
			writeError(w, http.StatusBadRequest, "insufficient funds")
			return
		}
		if err == ledger.ErrInvalidAmount {
			writeError(w, http.StatusBadRequest, "invalid amount")
			return
		}
		if err == ledger.ErrContention {
			writeError(w, http.StatusConflict, "contention")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to process withdrawal")
		return
	}

	writeJSON(w, http.StatusOK, transactionToResponse(txn))
}

// Transfer handles transfer requests
func (h *WalletHandler) Transfer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountIDStr := r.PathValue("account_id")

	fromAccountID, err := uuid.Parse(accountIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account_id")
		return
	}

	var req TransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	toAccountID, err := uuid.Parse(req.ToAccountID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid to_account_id")
		return
	}

	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid amount")
		return
	}

	idempotencyKey, ok := middleware.GetIdempotencyKey(ctx)
	if !ok {
		writeError(w, http.StatusBadRequest, "missing idempotency key")
		return
	}

	txn, err := h.walletService.Transfer(ctx, fromAccountID, toAccountID, amount, idempotencyKey, "Transfer")
	if err != nil {
		if err == ledger.ErrAccountNotFound {
			writeError(w, http.StatusNotFound, "account not found")
			return
		}
		if err == ledger.ErrInsufficientFunds {
			writeError(w, http.StatusBadRequest, "insufficient funds")
			return
		}
		if err == ledger.ErrSameAccount {
			writeError(w, http.StatusBadRequest, "cannot transfer to same account")
			return
		}
		if err == ledger.ErrCurrencyMismatch {
			writeError(w, http.StatusBadRequest, "currency mismatch")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to process transfer")
		return
	}

	writeJSON(w, http.StatusOK, transactionToResponse(txn))
}

// GetTransactions returns transactions for an account
func (h *WalletHandler) GetTransactions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountIDStr := r.PathValue("account_id")

	accountID, err := uuid.Parse(accountIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account_id")
		return
	}

	limit := int32(50)
	offset := int32(0)

	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = int32(parsed)
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = int32(parsed)
		}
	}

	txns, err := h.walletService.GetTransactions(ctx, accountID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get transactions")
		return
	}

	response := make([]TransactionResponse, len(txns))
	for i, txn := range txns {
		response[i] = *transactionToResponse(&txn)
	}

	writeJSON(w, http.StatusOK, response)
}

func transactionToResponse(txn *generated.Transaction) *TransactionResponse {
	resp := &TransactionResponse{
		ID:        uuidToString(txn.ID),
		Type:      txn.TransactionType,
		Status:    txn.Status,
		Amount:    txn.Amount.String(),
		Currency:  txn.Currency,
		CreatedAt: timestampToString(txn.CreatedAt),
	}
	if txn.SourceAccountID.Valid {
		s := uuidToString(txn.SourceAccountID)
		resp.SourceAccountID = &s
	}
	if txn.DestinationAccountID.Valid {
		s := uuidToString(txn.DestinationAccountID)
		resp.DestinationAccountID = &s
	}
	return resp
}

// Helper functions
func uuidToString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func fromPgUUID(p pgtype.UUID) uuid.UUID {
	if !p.Valid {
		return uuid.Nil
	}
	return p.Bytes
}

func timestampToString(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.Format("2006-01-02T15:04:05Z07:00")
}

// BatchOp is one operation in a batch request.
type BatchOp struct {
	Op        string `json:"op"`         // "deposit" | "withdraw"
	AccountID string `json:"account_id"`
	Amount    string `json:"amount"`
}

// BatchResult is the result of one batch operation.
type BatchResult struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// Batch executes multiple deposit/withdraw operations using a single pipelined
// connection per request. This eliminates goroutine thundering-herd on the pool
// and serialises intra-batch ops so they never contend on the same row lock.
// POST /v1/batch
func (h *WalletHandler) Batch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var ops []BatchOp
	if err := json.NewDecoder(r.Body).Decode(&ops); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(ops) == 0 {
		writeJSON(w, http.StatusOK, []BatchResult{})
		return
	}

	results := make([]BatchResult, len(ops))
	batchOps := make([]ledger.BatchOp, 0, len(ops))
	indices := make([]int, 0, len(ops))

	for i, op := range ops {
		accountID, err := uuid.Parse(op.AccountID)
		if err != nil {
			results[i] = BatchResult{Error: "invalid account_id"}
			continue
		}
		amount, err := decimal.NewFromString(op.Amount)
		if err != nil {
			results[i] = BatchResult{Error: "invalid amount"}
			continue
		}
		var opType ledger.BatchOpType
		switch op.Op {
		case "deposit":
			opType = ledger.BatchOpDeposit
		case "withdraw":
			opType = ledger.BatchOpWithdraw
		default:
			results[i] = BatchResult{Error: "unknown op"}
			continue
		}
		batchOps = append(batchOps, ledger.BatchOp{Type: opType, AccountID: accountID, Amount: amount})
		indices = append(indices, i)
	}

	if len(batchOps) > 0 {
		batchResults := h.walletService.ExecBatch(ctx, batchOps)
		for j, br := range batchResults {
			if br.Success {
				results[indices[j]] = BatchResult{Success: true}
			} else {
				results[indices[j]] = BatchResult{Error: br.Err.Error()}
			}
		}
	}

	writeJSON(w, http.StatusOK, results)
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// UserAccountResponse represents a user's account in the directory
type UserAccountResponse struct {
	ID       string `json:"id"`
	UserID   string `json:"user_id"`
	Currency string `json:"currency"`
	Balance  string `json:"balance"`
}

// GetAllUsers returns all user accounts in the system (for user directory)
func (h *WalletHandler) GetAllUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit := int32(1000)
	offset := int32(0)

	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 10000 {
			limit = int32(parsed)
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = int32(parsed)
		}
	}

	accounts, err := h.walletService.GetAllAccounts(ctx, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get users")
		return
	}

	response := make([]UserAccountResponse, len(accounts))
	for i, acc := range accounts {
		response[i] = UserAccountResponse{
			ID:       uuidToString(acc.ID),
			UserID:   acc.UserExternalID,
			Currency: acc.Currency,
			Balance:  acc.Balance.String(),
		}
	}

	writeJSON(w, http.StatusOK, response)
}
