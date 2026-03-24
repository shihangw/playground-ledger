package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"
)

var validModes = []string{"deposits", "withdrawals", "mixed", "credit-grants", "credit-drawdown", "credit-mixed"}

type stressOpts struct {
	apiURL         string
	mode           string
	concurrency    int
	duration       int
	rate           int
	prefix         string
	batchSize      int
	count          int
	initialBalance string
	runID          string
}

func cmdStress(args []string) {
	fs := flag.NewFlagSet("stress", flag.ExitOnError)
	apiURL := fs.String("api-url", "http://localhost:8080", "API base URL")
	mode := fs.String("mode", "mixed", "Test mode: "+strings.Join(validModes, ", "))
	concurrency := fs.Int("concurrency", 20, "Max concurrent requests")
	duration := fs.Int("duration", 60, "Test duration in seconds")
	rate := fs.Int("rate", 50, "Target requests per second")
	prefix := fs.String("prefix", "stress", "User ID prefix")
	batchSize := fs.Int("batch-size", 0, "Transactions per batch request (0 = single requests)")
	count := fs.Int("count", 100, "Users to create if none exist (auto-seed)")
	initialBalance := fs.String("initial-balance", "10000", "Starting balance for auto-seeded users")
	fs.Parse(args)

	isValid := false
	for _, m := range validModes {
		if *mode == m {
			isValid = true
			break
		}
	}
	if !isValid {
		fmt.Fprintf(os.Stderr, "Invalid mode. Use: %s\n", strings.Join(validModes, ", "))
		os.Exit(1)
	}

	runStress(stressOpts{
		apiURL:         *apiURL,
		mode:           *mode,
		concurrency:    *concurrency,
		duration:       *duration,
		rate:           *rate,
		prefix:         *prefix,
		batchSize:      *batchSize,
		count:          *count,
		initialBalance: *initialBalance,
		runID:          fmt.Sprintf("run_%d_%s", time.Now().UnixMilli(), randHex(6)),
	})
}

func runStress(opts stressOpts) {
	client := NewLedgerClient(opts.apiURL)

	batchStr := ""
	if opts.batchSize > 0 {
		batchStr = fmt.Sprintf(" batch=%d", opts.batchSize)
	}
	fmt.Printf("Stress test: mode=%s rate=%d/s concurrency=%d duration=%ds%s\n",
		opts.mode, opts.rate, opts.concurrency, opts.duration, batchStr)
	fmt.Printf("Run ID: %s\n", opts.runID)
	fmt.Printf("API: %s\n", opts.apiURL)

	fmt.Println("Fetching user accounts...")
	token := fmt.Sprintf("dev_%s_user_0", opts.prefix)

	accounts, err := fetchAllAccounts(client, token)
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "dial") {
			fmt.Fprintf(os.Stderr, "\nError: Cannot connect to API at %s\n", opts.apiURL)
			fmt.Fprintln(os.Stderr, "Is the server running? Start it with: cd go && go run cmd/api/main.go")
		} else {
			fmt.Fprintf(os.Stderr, "\nError fetching users: %v\n", err)
		}
		os.Exit(1)
	}

	if len(accounts) == 0 {
		fmt.Printf("No users found — seeding %d users with prefix %q...\n", opts.count, opts.prefix)
		if err := autoSeed(client, opts.count, opts.prefix, opts.initialBalance); err != nil {
			fmt.Fprintf(os.Stderr, "\nAuto-seed failed: %v\n", err)
			os.Exit(1)
		}
		accounts, err = fetchAllAccounts(client, token)
		if err != nil || len(accounts) == 0 {
			fmt.Fprintln(os.Stderr, "Still no users after seeding — something went wrong.")
			os.Exit(1)
		}
	}
	fmt.Printf("Found %d accounts\n", len(accounts))

	if opts.mode == "credit-drawdown" || opts.mode == "credit-mixed" {
		fmt.Println("Seeding initial grants for drawdown testing...")
		expiresAt := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
		seeded := 0
		for _, acc := range accounts {
			r := client.IssueGrant(acc.ID, "1000000.00", "PROMOTION", expiresAt, fmt.Sprintf("dev_%s", acc.UserID))
			if r.Success {
				seeded++
			}
		}
		fmt.Printf("Seeded grants for %d/%d accounts\n", seeded, len(accounts))
	}

	evBuf := &eventBuffer{runID: opts.runID, client: client}
	reporter := NewReporter()
	reporter.Start()

	runStressLoop(client, accounts, opts, reporter, evBuf)

	evBuf.Flush()
	reporter.Stop()
	reporter.PrintSummary()

	fmt.Printf("\nRun ID: %s\n", opts.runID)
	fmt.Printf("Query server-side metrics: ledger-stress metrics --run-id %s\n", opts.runID)
}

func fetchAllAccounts(client *LedgerClient, token string) ([]AccountInfo, error) {
	var all []AccountInfo
	offset := 0
	const pageSize = 1000
	for {
		page, err := client.GetUsers(token, pageSize, offset)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < pageSize {
			break
		}
		offset += pageSize
	}
	return all, nil
}

func autoSeed(client *LedgerClient, count int, prefix, initialBalance string) error {
	const batchSize = 10000
	remaining := count
	startIndex := 0
	totalCreated := 0
	for remaining > 0 {
		batch := remaining
		if batch > batchSize {
			batch = batchSize
		}
		result, err := client.Seed(batch, prefix, initialBalance, startIndex)
		if err != nil {
			return err
		}
		totalCreated += result.Created
		remaining -= batch
		startIndex += batch
		fmt.Printf("\r  seeded %d/%d...", totalCreated, count)
	}
	fmt.Printf("\nSeeded %d users\n", totalCreated)
	return nil
}

type eventBuffer struct {
	mu     sync.Mutex
	events []PendingEvent
	runID  string
	client *LedgerClient
}

func (b *eventBuffer) Add(e PendingEvent) {
	b.mu.Lock()
	b.events = append(b.events, e)
	shouldFlush := len(b.events) >= 100
	b.mu.Unlock()
	if shouldFlush {
		b.Flush()
	}
}

func (b *eventBuffer) Flush() {
	b.mu.Lock()
	if len(b.events) == 0 {
		b.mu.Unlock()
		return
	}
	batch := b.events
	b.events = nil
	b.mu.Unlock()
	b.client.LogEvents(batch) //nolint:errcheck
}

func runStressLoop(client *LedgerClient, accounts []AccountInfo, opts stressOpts, reporter *Reporter, evBuf *eventBuffer) {
	// Rate limiting: fire batchPerTick tasks every intervalMs milliseconds
	intervalMs := 1000 / opts.rate
	if intervalMs < 1 {
		intervalMs = 1
	}
	batchPerTick := (opts.rate*intervalMs + 999) / 1000
	if batchPerTick < 1 {
		batchPerTick = 1
	}

	// For batch mode, HTTP concurrency < txn concurrency
	httpConcurrency := opts.concurrency
	if opts.batchSize > 0 {
		httpConcurrency = opts.concurrency / opts.batchSize
		if httpConcurrency < 1 {
			httpConcurrency = 1
		}
	}

	sem := make(chan struct{}, httpConcurrency)
	ticker := time.NewTicker(time.Duration(intervalMs) * time.Millisecond)
	timer := time.NewTimer(time.Duration(opts.duration) * time.Second)
	defer ticker.Stop()
	defer timer.Stop()

	var wg sync.WaitGroup

loop:
	for {
		select {
		case <-timer.C:
			break loop
		case <-ticker.C:
			for i := 0; i < batchPerTick; i++ {
				select {
				case sem <- struct{}{}:
					wg.Add(1)
					go func() {
						defer wg.Done()
						defer func() { <-sem }()
						if opts.batchSize > 0 {
							runBatch(client, accounts, opts, reporter, evBuf)
						} else {
							runTask(client, accounts, opts, reporter, evBuf)
						}
					}()
				default:
					// at concurrency limit, skip this slot
				}
			}
		}
	}

	wg.Wait()
}

func pickRandom(accounts []AccountInfo) AccountInfo {
	return accounts[rand.Intn(len(accounts))]
}

func pickTaskType(mode string) string {
	switch mode {
	case "deposits":
		return "deposit"
	case "withdrawals":
		return "withdraw"
	case "mixed":
		if rand.Float64() < 0.3 {
			return "deposit"
		}
		return "withdraw"
	case "credit-grants":
		return "grant-issue"
	case "credit-drawdown":
		return "grant-drawdown"
	case "credit-mixed":
		if rand.Float64() < 0.3 {
			return "grant-issue"
		}
		return "grant-drawdown"
	default:
		return "deposit"
	}
}

func runTask(client *LedgerClient, accounts []AccountInfo, opts stressOpts, reporter *Reporter, evBuf *eventBuffer) {
	reporter.RecordHTTP()
	acc := pickRandom(accounts)
	taskType := pickTaskType(opts.mode)
	userToken := fmt.Sprintf("dev_%s", acc.UserID)

	var result StressResult
	var eventType string

	switch taskType {
	case "deposit":
		eventType = "DEPOSIT"
		amount := fmt.Sprintf("%.2f", rand.Float64()*490+10)
		result = client.Deposit(acc.ID, amount, userToken)
	case "withdraw":
		eventType = "WITHDRAWAL"
		amount := fmt.Sprintf("%.2f", rand.Float64()*0.99+0.01)
		result = client.Withdraw(acc.ID, amount, userToken)
	case "grant-issue":
		eventType = "GRANT_ISSUE"
		amount := fmt.Sprintf("%.2f", rand.Float64()*490+10)
		expiresAt := time.Now().Add(time.Duration(rand.Intn(29)+1) * 24 * time.Hour).UTC().Format(time.RFC3339)
		grantTypes := []string{"SIGNUP_BONUS", "PROMOTION", "MANUAL"}
		result = client.IssueGrant(acc.ID, amount, grantTypes[rand.Intn(len(grantTypes))], expiresAt, userToken)
	case "grant-drawdown":
		eventType = "GRANT_DRAWDOWN"
		amount := fmt.Sprintf("%.2f", rand.Float64()*0.99+0.01)
		result = client.DrawdownGrant(acc.ID, amount, userToken)
	}

	reporter.Record(result.Success, result.LatencyMs, result.ErrorType)
	evBuf.Add(PendingEvent{
		RunID:        opts.runID,
		EventType:    eventType,
		AccountID:    acc.ID,
		Success:      result.Success,
		LatencyMs:    result.LatencyMs,
		ErrorMessage: result.ErrorType,
	})
}

func runBatch(client *LedgerClient, accounts []AccountInfo, opts stressOpts, reporter *Reporter, evBuf *eventBuffer) {
	ops := make([]BatchOp, opts.batchSize)
	opAccounts := make([]AccountInfo, opts.batchSize)
	for i := range ops {
		acc := pickRandom(accounts)
		taskType := pickTaskType(opts.mode)
		var op, amount string
		if taskType == "deposit" || taskType == "grant-issue" {
			op = "deposit"
			amount = fmt.Sprintf("%.2f", rand.Float64()*490+10)
		} else {
			op = "withdraw"
			amount = fmt.Sprintf("%.2f", rand.Float64()*0.99+0.01)
		}
		ops[i] = BatchOp{Op: op, AccountID: acc.ID, Amount: amount}
		opAccounts[i] = acc
	}

	t0 := time.Now()
	reporter.RecordHTTP()
	results, err := client.Batch(ops)
	latencyMs := float64(time.Since(t0)) / float64(time.Millisecond)

	if err != nil {
		for i, op := range ops {
			reporter.Record(false, latencyMs, "other")
			evBuf.Add(PendingEvent{
				RunID:        opts.runID,
				EventType:    strings.ToUpper(op.Op),
				AccountID:    opAccounts[i].ID,
				Success:      false,
				LatencyMs:    latencyMs,
				ErrorMessage: "other",
			})
		}
		return
	}

	for i, r := range results {
		errType := ""
		if !r.Success {
			errType = classifyBatchError(r.Error)
		}
		reporter.Record(r.Success, latencyMs, errType)
		evBuf.Add(PendingEvent{
			RunID:        opts.runID,
			EventType:    strings.ToUpper(ops[i].Op),
			AccountID:    opAccounts[i].ID,
			Success:      r.Success,
			LatencyMs:    latencyMs,
			ErrorMessage: errType,
		})
	}
}

func classifyBatchError(msg string) string {
	if strings.Contains(msg, "insufficient grant") {
		return "insufficient_grants"
	}
	if strings.Contains(msg, "insufficient funds") {
		return "insufficient_funds"
	}
	if strings.Contains(msg, "contention") || strings.Contains(msg, "deadlock") || strings.Contains(msg, "lock timeout") {
		return "contention"
	}
	return "other"
}

func randHex(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}
