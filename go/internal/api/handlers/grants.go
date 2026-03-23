package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/shihangw/playground-ledger/internal/api/middleware"
	"github.com/shihangw/playground-ledger/internal/db/generated"
	"github.com/shihangw/playground-ledger/internal/grants"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// GrantsHandler handles grant-related HTTP requests
type GrantsHandler struct {
	grantsService *grants.Service
}

// NewGrantsHandler creates a new grants handler
func NewGrantsHandler(grantsService *grants.Service) *GrantsHandler {
	return &GrantsHandler{
		grantsService: grantsService,
	}
}

// IssueGrantRequest represents a grant issuance request body
type IssueGrantRequest struct {
	Amount    string `json:"amount"`
	GrantType string `json:"grant_type"`
	ExpiresAt string `json:"expires_at"` // RFC3339
	Metadata  []byte `json:"metadata,omitempty"`
}

// DrawdownRequest represents a drawdown request body
type DrawdownRequest struct {
	Amount string `json:"amount"`
}

// GrantResponse represents a grant in API responses
type GrantResponse struct {
	ID              string `json:"id"`
	AccountID       string `json:"account_id"`
	GrantType       string `json:"grant_type"`
	InitialAmount   string `json:"initial_amount"`
	RemainingAmount string `json:"remaining_amount"`
	ExpiresAt       string `json:"expires_at"`
	Status          string `json:"status"`
	CreatedAt       string `json:"created_at"`
}

// IssueGrant handles grant issuance requests
func (h *GrantsHandler) IssueGrant(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountIDStr := r.PathValue("account_id")

	accountID, err := uuid.Parse(accountIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account_id")
		return
	}

	var req IssueGrantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid amount")
		return
	}

	validTypes := map[string]bool{"SIGNUP_BONUS": true, "PROMOTION": true, "MANUAL": true}
	if !validTypes[req.GrantType] {
		writeError(w, http.StatusBadRequest, "invalid grant_type: must be SIGNUP_BONUS, PROMOTION, or MANUAL")
		return
	}

	expiresAt, err := time.Parse(time.RFC3339, req.ExpiresAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid expires_at: must be RFC3339 format")
		return
	}

	if expiresAt.Before(time.Now()) {
		writeError(w, http.StatusBadRequest, "expires_at must be in the future")
		return
	}

	idempotencyKey, ok := middleware.GetIdempotencyKey(ctx)
	if !ok {
		writeError(w, http.StatusBadRequest, "missing idempotency key")
		return
	}

	grant, err := h.grantsService.IssueGrant(ctx, grants.IssueGrantRequest{
		AccountID:      accountID,
		Amount:         amount,
		GrantType:      req.GrantType,
		ExpiresAt:      expiresAt,
		IdempotencyKey: idempotencyKey,
		Metadata:       req.Metadata,
	})
	if err != nil {
		if err == grants.ErrAccountNotFound {
			writeError(w, http.StatusNotFound, "account not found")
			return
		}
		if err == grants.ErrInvalidAmount {
			writeError(w, http.StatusBadRequest, "invalid amount")
			return
		}
		log.Printf("Failed to issue grant: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to issue grant")
		return
	}

	writeJSON(w, http.StatusCreated, grantToResponse(grant))
}

// Drawdown handles credit consumption requests
func (h *GrantsHandler) Drawdown(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountIDStr := r.PathValue("account_id")

	accountID, err := uuid.Parse(accountIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account_id")
		return
	}

	var req DrawdownRequest
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

	results, err := h.grantsService.Drawdown(ctx, accountID, amount, idempotencyKey)
	if err != nil {
		if err == grants.ErrInsufficientGrants {
			writeError(w, http.StatusBadRequest, "insufficient grant balance")
			return
		}
		if err == grants.ErrInvalidAmount {
			writeError(w, http.StatusBadRequest, "invalid amount")
			return
		}
		log.Printf("Failed to drawdown: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to process drawdown")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"drawdowns":      results,
		"total_consumed": amount.String(),
	})
}

// ListGrants returns grants for an account
func (h *GrantsHandler) ListGrants(w http.ResponseWriter, r *http.Request) {
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

	grantsList, err := h.grantsService.GetGrants(ctx, accountID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get grants")
		return
	}

	response := make([]GrantResponse, len(grantsList))
	for i, g := range grantsList {
		response[i] = *grantToResponse(&g)
	}

	writeJSON(w, http.StatusOK, response)
}

// GetBalance returns the available grant balance for an account
func (h *GrantsHandler) GetBalance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountIDStr := r.PathValue("account_id")

	accountID, err := uuid.Parse(accountIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account_id")
		return
	}

	balance, err := h.grantsService.GetAvailableBalance(ctx, accountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get grant balance")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"account_id":        accountID.String(),
		"available_balance": balance.String(),
	})
}

// ExpireGrants triggers expiration of past-due grants
func (h *GrantsHandler) ExpireGrants(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	count, err := h.grantsService.ExpireGrants(ctx)
	if err != nil {
		log.Printf("Failed to expire grants: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to expire grants")
		return
	}

	writeJSON(w, http.StatusOK, map[string]int64{
		"expired_count": count,
	})
}

func grantToResponse(g *generated.CreditGrant) *GrantResponse {
	return &GrantResponse{
		ID:              uuidToString(g.ID),
		AccountID:       uuidToString(g.AccountID),
		GrantType:       g.GrantType,
		InitialAmount:   g.InitialAmount.String(),
		RemainingAmount: g.RemainingAmount.String(),
		ExpiresAt:       timestampToString(g.ExpiresAt),
		Status:          g.Status,
		CreatedAt:       timestampToString(g.CreatedAt),
	}
}
