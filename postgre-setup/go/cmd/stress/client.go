package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	requestTimeout = 10 * time.Second
	seedTimeout    = 30 * time.Second
)

type LedgerClient struct {
	baseURL string
	http    *http.Client
}

func NewLedgerClient(baseURL string) *LedgerClient {
	return &LedgerClient{
		baseURL: baseURL,
		http: &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:        1000,
				MaxIdleConnsPerHost: 1000,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

type AccountInfo struct {
	ID       string `json:"id"`
	UserID   string `json:"user_id"`
	Currency string `json:"currency"`
	Balance  string `json:"balance"`
}

type SeedResponse struct {
	Created int      `json:"created"`
	Errors  []string `json:"errors"`
}

type StressResult struct {
	Success   bool
	LatencyMs float64
	Error     string
	ErrorType string // insufficient_funds, insufficient_grants, contention, timeout, other
}

type BatchOp struct {
	Op        string `json:"op"`
	AccountID string `json:"account_id"`
	Amount    string `json:"amount"`
}

type BatchResult struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

type PendingEvent struct {
	RunID        string  `json:"run_id"`
	EventType    string  `json:"event_type"`
	AccountID    string  `json:"account_id"`
	Success      bool    `json:"success"`
	LatencyMs    float64 `json:"latency_ms"`
	ErrorMessage string  `json:"error_message,omitempty"`
}

type ReconcileResponse struct {
	OK               bool `json:"ok"`
	DiscrepancyCount int  `json:"discrepancy_count"`
	Discrepancies    []struct {
		AccountID      string `json:"account_id"`
		AccountBalance string `json:"account_balance"`
		LedgerBalance  string `json:"ledger_balance"`
		Discrepancy    string `json:"discrepancy"`
		EntryCount     int    `json:"entry_count"`
	} `json:"discrepancies"`
}

func (c *LedgerClient) Seed(count int, prefix, initialBalance string, startIndex int) (SeedResponse, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"count":           count,
		"prefix":          prefix,
		"initial_balance": initialBalance,
		"start_index":     startIndex,
	})
	ctx, cancel := context.WithTimeout(context.Background(), seedTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/admin/seed", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return SeedResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return SeedResponse{}, fmt.Errorf("seed failed (%d): %s", resp.StatusCode, b)
	}
	var result SeedResponse
	json.NewDecoder(resp.Body).Decode(&result)
	return result, nil
}

func (c *LedgerClient) GetUsers(token string, limit, offset int) ([]AccountInfo, error) {
	url := fmt.Sprintf("%s/v1/users?limit=%d&offset=%d", c.baseURL, limit, offset)
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get users failed (%d)", resp.StatusCode)
	}
	var accounts []AccountInfo
	json.NewDecoder(resp.Body).Decode(&accounts)
	return accounts, nil
}

func (c *LedgerClient) GetAllUsers(limit int) ([]AccountInfo, error) {
	return c.GetUsers("dev_stress_user_1", limit, 0)
}

// doTxn executes a single transactional HTTP request and returns a StressResult.
func (c *LedgerClient) doTxn(method, url, token string, body interface{}) StressResult {
	bodyBytes, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", uuid.New().String())
	start := time.Now()
	resp, err := c.http.Do(req)
	latencyMs := float64(time.Since(start)) / float64(time.Millisecond)
	if err != nil {
		return StressResult{LatencyMs: latencyMs, ErrorType: "timeout", Error: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var errBody map[string]string
		json.NewDecoder(resp.Body).Decode(&errBody)
		msg := errBody["error"]
		return StressResult{LatencyMs: latencyMs, Error: msg, ErrorType: classifyError(resp.StatusCode, msg)}
	}
	io.Copy(io.Discard, resp.Body)
	return StressResult{Success: true, LatencyMs: latencyMs}
}

func (c *LedgerClient) Deposit(accountID, amount, token string) StressResult {
	return c.doTxn("POST",
		fmt.Sprintf("%s/v1/accounts/%s/deposit", c.baseURL, accountID),
		token,
		map[string]string{"amount": amount, "currency": "USD"},
	)
}

func (c *LedgerClient) Withdraw(accountID, amount, token string) StressResult {
	return c.doTxn("POST",
		fmt.Sprintf("%s/v1/accounts/%s/withdraw", c.baseURL, accountID),
		token,
		map[string]string{"amount": amount},
	)
}

func (c *LedgerClient) IssueGrant(accountID, amount, grantType, expiresAt, token string) StressResult {
	return c.doTxn("POST",
		fmt.Sprintf("%s/v1/accounts/%s/grants", c.baseURL, accountID),
		token,
		map[string]string{"amount": amount, "grant_type": grantType, "expires_at": expiresAt},
	)
}

func (c *LedgerClient) DrawdownGrant(accountID, amount, token string) StressResult {
	return c.doTxn("POST",
		fmt.Sprintf("%s/v1/accounts/%s/grants/drawdown", c.baseURL, accountID),
		token,
		map[string]string{"amount": amount},
	)
}

func (c *LedgerClient) Transfer(fromID, toID, amount, token string) StressResult {
	return c.doTxn("POST",
		fmt.Sprintf("%s/v1/accounts/%s/transfer", c.baseURL, fromID),
		token,
		map[string]string{"to_account_id": toID, "amount": amount},
	)
}

func (c *LedgerClient) Batch(ops []BatchOp) ([]BatchResult, error) {
	body, _ := json.Marshal(ops)
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("batch failed (%d)", resp.StatusCode)
	}
	var results []BatchResult
	json.NewDecoder(resp.Body).Decode(&results)
	return results, nil
}

func (c *LedgerClient) LogEvents(events []PendingEvent) error {
	body, _ := json.Marshal(map[string]interface{}{"events": events})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/admin/stress/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

func (c *LedgerClient) ListRuns(limit int) ([]map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("%s/v1/admin/stress/runs?limit=%d", c.baseURL, limit), nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list runs failed (%d)", resp.StatusCode)
	}
	var runs []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&runs)
	return runs, nil
}

func (c *LedgerClient) GetRunSummary(runID string) (map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("%s/v1/admin/stress/runs/%s", c.baseURL, runID), nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get run summary failed (%d)", resp.StatusCode)
	}
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	return result, nil
}

func (c *LedgerClient) Reconcile(sample int) (ReconcileResponse, error) {
	url := c.baseURL + "/v1/admin/reconcile"
	if sample > 0 {
		url += fmt.Sprintf("?sample=%d", sample)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return ReconcileResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return ReconcileResponse{}, fmt.Errorf("reconcile failed (%d): %s", resp.StatusCode, b)
	}
	var result ReconcileResponse
	json.NewDecoder(resp.Body).Decode(&result)
	return result, nil
}

func classifyError(status int, msg string) string {
	if strings.Contains(msg, "insufficient grant") {
		return "insufficient_grants"
	}
	if strings.Contains(msg, "insufficient funds") {
		return "insufficient_funds"
	}
	if status == 409 {
		return "contention"
	}
	if status == 408 || status == 504 {
		return "timeout"
	}
	return "other"
}
