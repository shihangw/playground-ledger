package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"

	"github.com/shihangw/playground-ledger/internal/wallet"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// AdminHandler handles admin/testing HTTP requests
type AdminHandler struct {
	walletService *wallet.Service
	pool          *pgxpool.Pool
}

// NewAdminHandler creates a new admin handler
func NewAdminHandler(walletService *wallet.Service, pool *pgxpool.Pool) *AdminHandler {
	return &AdminHandler{walletService: walletService, pool: pool}
}

// ReconcileRow is one account's reconciliation result.
type ReconcileRow struct {
	AccountID      string `json:"account_id"`
	AccountBalance string `json:"account_balance"`
	LedgerBalance  string `json:"ledger_balance"`
	Discrepancy    string `json:"discrepancy"`
	EntryCount     int64  `json:"entry_count"`
}

// Reconcile checks the balance invariant: accounts.balance == SUM(CREDIT) - SUM(DEBIT)
// across all accounts. Returns only rows with discrepancies.
// GET /v1/admin/reconcile?limit=100&sample=1000
func (h *AdminHandler) Reconcile(w http.ResponseWriter, r *http.Request) {
	limit := int64(100)
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.ParseInt(l, 10, 64); err == nil && n > 0 {
			limit = n
		}
	}

	// Optional: restrict check to a random sample of accounts for speed.
	sampleClause := ""
	if s := r.URL.Query().Get("sample"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
			sampleClause = fmt.Sprintf("WHERE a.id IN (SELECT id FROM accounts ORDER BY random() LIMIT %d)", n)
		}
	}

	sql := fmt.Sprintf(`
SELECT
    a.id::text                                                                                   AS account_id,
    a.balance::text                                                                              AS account_balance,
    COALESCE(SUM(CASE WHEN le.entry_type = 'CREDIT' THEN le.amount ELSE -le.amount END), 0)::text AS ledger_balance,
    (a.balance - COALESCE(SUM(CASE WHEN le.entry_type = 'CREDIT' THEN le.amount ELSE -le.amount END), 0))::text AS discrepancy,
    COUNT(le.id)                                                                                 AS entry_count
FROM accounts a %s
LEFT JOIN ledger_entries le ON le.account_id = a.id
GROUP BY a.id, a.balance
HAVING a.balance != COALESCE(SUM(CASE WHEN le.entry_type = 'CREDIT' THEN le.amount ELSE -le.amount END), 0)
ORDER BY ABS(a.balance - COALESCE(SUM(CASE WHEN le.entry_type = 'CREDIT' THEN le.amount ELSE -le.amount END), 0)) DESC
LIMIT $1`, sampleClause)

	rows, err := h.pool.Query(context.Background(), sql, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("query failed: %v", err))
		return
	}
	defer rows.Close()

	var discrepancies []ReconcileRow
	for rows.Next() {
		var row ReconcileRow
		if err := rows.Scan(&row.AccountID, &row.AccountBalance, &row.LedgerBalance, &row.Discrepancy, &row.EntryCount); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("scan failed: %v", err))
			return
		}
		discrepancies = append(discrepancies, row)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("rows error: %v", err))
		return
	}

	if discrepancies == nil {
		discrepancies = []ReconcileRow{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"discrepancy_count": len(discrepancies),
		"discrepancies":     discrepancies,
		"ok":                len(discrepancies) == 0,
	})
}

// SeedRequest represents a request to seed test users
type SeedRequest struct {
	Count          int    `json:"count"`
	Prefix         string `json:"prefix"`
	InitialBalance string `json:"initial_balance"`
	StartIndex     int    `json:"start_index"`
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

	if req.Count <= 0 || req.Count > 10000 {
		writeError(w, http.StatusBadRequest, "count must be between 1 and 10000")
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

			extID := fmt.Sprintf("%s_user_%d", req.Prefix, req.StartIndex+i)
			email := fmt.Sprintf("%s_user_%d@stress.local", req.Prefix, req.StartIndex+i)

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
