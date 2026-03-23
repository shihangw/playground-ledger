package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

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

	log.Printf("Seeding %d users with prefix %q and balance %s", req.Count, req.Prefix, initialBalance)

	type indexedResult struct {
		index  int
		result *SeedResult
		err    string
	}

	ch := make(chan indexedResult, req.Count)
	var wg sync.WaitGroup
	concurrency := 10
	sem := make(chan struct{}, concurrency)

	for i := 0; i < req.Count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			extID := fmt.Sprintf("%s_user_%d", req.Prefix, i)
			email := fmt.Sprintf("%s_user_%d@stress.local", req.Prefix, i)

			user, err := h.walletService.GetOrCreateUser(r.Context(), extID, email)
			if err != nil {
				ch <- indexedResult{i, nil, fmt.Sprintf("user %d: %v", i, err)}
				return
			}

			accounts, err := h.walletService.GetAccountsByUser(r.Context(), user.ID.Bytes)
			if err != nil || len(accounts) == 0 {
				ch <- indexedResult{i, nil, fmt.Sprintf("user %d: no accounts", i)}
				return
			}

			acc := accounts[0]

			if acc.Balance.LessThan(initialBalance) {
				topUp := initialBalance.Sub(acc.Balance)
				idempotencyKey := wallet.SeedIdempotencyKey(extID, initialBalance)
				_, err = h.walletService.Deposit(r.Context(), acc.ID.Bytes, topUp, idempotencyKey, "Seed top-up")
				if err != nil {
					ch <- indexedResult{i, nil, fmt.Sprintf("user %d deposit: %v", i, err)}
					return
				}
			}

			bal, _ := h.walletService.GetBalance(r.Context(), acc.ID.Bytes)

			ch <- indexedResult{i, &SeedResult{
				ExternalID: extID,
				Email:      email,
				AccountID:  uuidToString(acc.ID),
				Balance:    bal.String(),
			}, ""}

			if (i+1)%10 == 0 || i+1 == req.Count {
				log.Printf("  seeded %d/%d users", i+1, req.Count)
			}
		}(i)
	}

	go func() { wg.Wait(); close(ch) }()

	results := make([]SeedResult, 0, req.Count)
	var errors []string
	for r := range ch {
		if r.err != "" {
			errors = append(errors, r.err)
		} else {
			results = append(results, *r.result)
		}
	}

	log.Printf("Seeding complete: %d created, %d errors", len(results), len(errors))

	resp := map[string]interface{}{
		"created": len(results),
		"users":   results,
	}
	if len(errors) > 0 {
		resp["errors"] = errors
	}

	writeJSON(w, http.StatusOK, resp)
}
