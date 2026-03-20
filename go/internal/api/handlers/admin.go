package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/shihangw/playground-ledger/internal/wallet"
	"github.com/shopspring/decimal"
)

// AdminHandler handles admin/testing HTTP requests
type AdminHandler struct {
	walletService *wallet.Service
}

// NewAdminHandler creates a new admin handler
func NewAdminHandler(walletService *wallet.Service) *AdminHandler {
	return &AdminHandler{walletService: walletService}
}

// SeedRequest represents a request to seed test users
type SeedRequest struct {
	Count          int    `json:"count"`
	Prefix         string `json:"prefix"`
	InitialBalance string `json:"initial_balance"`
}

// SeedResult represents one seeded user
type SeedResult struct {
	ExternalID string `json:"external_id"`
	Email      string `json:"email"`
	AccountID  string `json:"account_id"`
	Balance    string `json:"balance"`
}

// Seed creates test users with accounts and initial balances
func (h *AdminHandler) Seed(w http.ResponseWriter, r *http.Request) {
	var req SeedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Count <= 0 || req.Count > 1000 {
		writeError(w, http.StatusBadRequest, "count must be between 1 and 1000")
		return
	}
	if req.Prefix == "" {
		req.Prefix = "stress"
	}

	initialBalance := decimal.NewFromInt(10000)
	if req.InitialBalance != "" {
		var err error
		initialBalance, err = decimal.NewFromString(req.InitialBalance)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid initial_balance")
			return
		}
	}

	results := make([]SeedResult, 0, req.Count)
	var errors []string

	for i := 0; i < req.Count; i++ {
		extID := fmt.Sprintf("%s_user_%d", req.Prefix, i)
		email := fmt.Sprintf("%s_user_%d@stress.local", req.Prefix, i)

		user, err := h.walletService.GetOrCreateUser(r.Context(), extID, email)
		if err != nil {
			errors = append(errors, fmt.Sprintf("user %d: %v", i, err))
			continue
		}

		accounts, err := h.walletService.GetAccountsByUser(r.Context(), user.ID.Bytes)
		if err != nil || len(accounts) == 0 {
			errors = append(errors, fmt.Sprintf("user %d: no accounts", i))
			continue
		}

		acc := accounts[0]
		currentBalance := acc.Balance

		// Top up to initial_balance if current balance is lower
		if currentBalance.LessThan(initialBalance) {
			topUp := initialBalance.Sub(currentBalance)
			idempotencyKey := wallet.SeedIdempotencyKey(extID, initialBalance)
			_, err = h.walletService.Deposit(r.Context(), acc.ID.Bytes, topUp, idempotencyKey, "Seed top-up")
			if err != nil {
				errors = append(errors, fmt.Sprintf("user %d deposit: %v", i, err))
			}
		}

		// Re-fetch balance
		bal, _ := h.walletService.GetBalance(r.Context(), acc.ID.Bytes)

		results = append(results, SeedResult{
			ExternalID: extID,
			Email:      email,
			AccountID:  uuidToString(acc.ID),
			Balance:    bal.String(),
		})
	}

	resp := map[string]interface{}{
		"created": len(results),
		"users":   results,
	}
	if len(errors) > 0 {
		resp["errors"] = errors
	}

	writeJSON(w, http.StatusOK, resp)
}
