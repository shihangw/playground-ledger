// bench — direct-to-DB benchmark for the ledger service.
//
// Unlike the HTTP stress tool, this binary bypasses the API server entirely:
// it calls ledger.Service methods directly over a pgxpool connection to
// AlloyDB. The result is pure DB transaction throughput with no HTTP, JSON,
// auth, or middleware overhead.
//
// Three scenarios (mirrors the TB-style benchmark in cmd/stress):
//
//	1. Waterfall   — N goroutines, each owning a 4-account group; waterfall
//	                 cascade on insufficient_funds.  Zero contention.
//	2. Hot account — all goroutines debit the same source account.
//	                 Maximum row-lock contention.
//	3. Fan-out     — each job is ExecBatch(1 000 deposits) via the pgx
//	                 pipeline protocol.  1 job = 1 000 DB transactions.
//
// Usage:
//
//	go run ./cmd/bench [flags]
//
// Flags:
//
//	--scenario   0=all 1=waterfall 2=hot 3=fanout  (default 0)
//	--concurrency                                   (default 32)
//	--duration   per-scenario                       (default 15s)
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/shihangw/playground-ledger/internal/config"
	"github.com/shihangw/playground-ledger/internal/db/generated"
	"github.com/shihangw/playground-ledger/internal/ledger"
)

func main() {
	scenario := flag.Int("scenario", 0, "0=all 1=waterfall 2=hot-account 3=fanout")
	concurrency := flag.Int("concurrency", 32, "Goroutines per scenario")
	duration := flag.Duration("duration", 15*time.Second, "Duration per scenario")
	flag.Parse()

	if *concurrency < 1 {
		fmt.Fprintln(os.Stderr, "--concurrency must be >= 1")
		os.Exit(1)
	}

	ctx := context.Background()

	cfg, err := config.Load(ctx)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.ActiveDSN())
	if err != nil {
		log.Fatalf("parse DSN: %v", err)
	}
	poolCfg.MaxConns = int32(cfg.DBPoolSize)
	poolCfg.MinConns = int32(cfg.DBMinConns)
	if cfg.DBSimpleProtocol {
		poolCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	}
	poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET lock_timeout = '200ms'")
		return err
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("ping: %v", err)
	}

	svc := ledger.NewService(pool)
	q := generated.New(pool)

	fmt.Printf("Direct-to-DB benchmark (no HTTP)\n")
	fmt.Printf("AlloyDB  concurrency=%d  duration=%s\n\n", *concurrency, *duration)

	accounts := fetchAccounts(ctx, q, *concurrency)

	var results []scenarioResult

	if *scenario == 0 || *scenario == 1 {
		fmt.Println("Scenario 1 — Waterfall withdrawal (independent accounts)...")
		results = append(results, runWaterfall(ctx, pool, accounts, *concurrency, *duration))
		fmt.Println()
	}
	if *scenario == 0 || *scenario == 2 {
		fmt.Println("Scenario 2 — Hot account withdrawal...")
		results = append(results, runHotAccount(ctx, svc, accounts, *concurrency, *duration))
		fmt.Println()
	}
	if *scenario == 0 || *scenario == 3 {
		fmt.Println("Scenario 3 — Fan-out to 1 000 accounts...")
		results = append(results, runFanOut(ctx, svc, accounts, *concurrency, *duration))
		fmt.Println()
	}

	printResults(results)
}

// ── Account loading ───────────────────────────────────────────────────────────

// fetchAccounts loads account UUIDs from the DB, auto-failing if there are not
// enough for the requested concurrency.  Returns IDs as uuid.UUID.
func fetchAccounts(ctx context.Context, q *generated.Queries, concurrency int) []uuid.UUID {
	minNeeded := 4*concurrency + 1000 // worst case: S1 needs 4×, S3 needs +1000 for destinations
	fmt.Printf("Fetching accounts (need ≥%d)...\n", minNeeded)

	var ids []uuid.UUID
	const pageSize = 5000
	for offset := 0; ; offset += pageSize {
		rows, err := q.GetAllAccounts(ctx, generated.GetAllAccountsParams{
			Limit:  int32(pageSize),
			Offset: int32(offset),
		})
		if err != nil {
			log.Fatalf("GetAllAccounts: %v", err)
		}
		for _, r := range rows {
			ids = append(ids, r.ID.Bytes)
		}
		if len(rows) < pageSize {
			break
		}
	}

	if len(ids) < minNeeded {
		fmt.Fprintf(os.Stderr,
			"Not enough accounts: found %d, need %d.\n"+
				"Seed more accounts first:  go run ./cmd/stress/ seed --count %d\n",
			len(ids), minNeeded, minNeeded)
		os.Exit(1)
	}
	fmt.Printf("Loaded %d account IDs\n\n", len(ids))
	return ids
}

// ── Metrics ───────────────────────────────────────────────────────────────────

type scenarioResult struct {
	name     string
	jobs     int64
	errors   int64
	txns     int64
	duration time.Duration
	lats     []float64
}

func (r *scenarioResult) jobsPerSec() float64 { return float64(r.jobs) / r.duration.Seconds() }
func (r *scenarioResult) txnsPerSec() float64 { return float64(r.txns) / r.duration.Seconds() }

type collector struct {
	mu   sync.Mutex
	lats []float64
	jobs atomic.Int64
	errs atomic.Int64
	txns atomic.Int64
}

func (c *collector) record(latMs float64, success bool, txnCount int) {
	c.mu.Lock()
	c.lats = append(c.lats, latMs)
	c.mu.Unlock()
	if success {
		c.jobs.Add(1)
	} else {
		c.errs.Add(1)
	}
	c.txns.Add(int64(txnCount))
}

func (c *collector) result(name string, dur time.Duration) scenarioResult {
	c.mu.Lock()
	lats := make([]float64, len(c.lats))
	copy(lats, c.lats)
	c.mu.Unlock()
	return scenarioResult{
		name: name, jobs: c.jobs.Load(), errors: c.errs.Load(),
		txns: c.txns.Load(), duration: dur, lats: lats,
	}
}

// ── Scenario 1: Waterfall ─────────────────────────────────────────────────────
//
// Each goroutine owns an exclusive 4-account group modelling the priority order:
//   daily credit → monthly credit → bonus credit → cash
//
// Balances: $5 on the 3 priority accounts, $10 000 on the cash account.
// Each account handles exactly 500 draws at $0.01 before depleting.
// Once depleted, depleted_at is set and the waterfall skips it without a
// DB round-trip. After waterfallTopupEvery successful draws the goroutine
// tops up the 3 priority accounts back to $5 (simulating a daily credit reset).
//
// The entire cascade is a single BEGIN…COMMIT — exactly 1 DB transaction per draw.

// 1 unit = $0.10
// A/B/C: 50 units ($5) each — deplete after 50 draws
// D (cash): 1 000 000 units ($100 000) — safety net, never topped up
// Top-up resets A/B/C to $5 every 150 draws (= 3 × 50, one full cascade cycle)
const (
	waterfallDebitAmt   = "0.10"
	waterfallSmallBal   = "5"      // 50 units × $0.10
	waterfallCashBal    = "100000" // 1 000 000 units × $0.10
	waterfallTopupEvery = 150      // 3 accounts × 50 draws per cycle
)

// waterfallOptimisticSQL tries the first-priority account with a single
// auto-committed CTE — no explicit BEGIN/COMMIT round-trip.
// RowsAffected on the INSERT is 1 on success, 0 if the account had no funds
// or was already depleted.
const waterfallOptimisticSQL = `
	WITH debit AS (
		UPDATE accounts
		SET balance    = balance - $1,
		    updated_at = now(),
		    depleted_at = CASE WHEN balance - $1 = 0 THEN now() ELSE NULL END
		WHERE id = $2 AND balance >= $1 AND depleted_at IS NULL
		RETURNING balance
	)
	INSERT INTO ledger_entries (account_id, entry_type, amount, balance_after)
	SELECT $2, 'DEBIT', $1, balance FROM debit`

// waterfallDebitSQL is used inside the fallback explicit transaction for
// accounts[1..3].
const waterfallDebitSQL = `
	UPDATE accounts
	SET balance    = balance - $1,
	    updated_at = now(),
	    depleted_at = CASE WHEN balance - $1 = 0 THEN now() ELSE NULL END
	WHERE id = $2 AND balance >= $1 AND depleted_at IS NULL
	RETURNING balance`

const waterfallEntrySQL = `
	INSERT INTO ledger_entries (account_id, entry_type, amount, balance_after)
	VALUES ($1, 'DEBIT', $2, $3)`

const waterfallSetupSQL = `
	UPDATE accounts SET balance = $1, depleted_at = NULL, updated_at = now()
	WHERE id = $2`

// setupWaterfallBalances initialises each 4-account group to realistic starting
// balances: $5 for the three priority accounts, $10 000 for the cash fallback.
func setupWaterfallBalances(ctx context.Context, pool *pgxpool.Pool, groups [][]uuid.UUID) {
	small := decimal.RequireFromString(waterfallSmallBal)
	cash := decimal.RequireFromString(waterfallCashBal)
	for _, group := range groups {
		for i, id := range group {
			bal := small
			if i == len(group)-1 {
				bal = cash
			}
			pool.Exec(ctx, waterfallSetupSQL, bal, id) //nolint:errcheck
		}
	}
	fmt.Printf("  Setup: %d groups — priority accounts $%s, cash $%s\n",
		len(groups), waterfallSmallBal, waterfallCashBal)
}

// topupPriorityAccounts resets the three priority accounts in a group back to
// $5 and clears their depleted_at flag, simulating a periodic credit refill.
func topupPriorityAccounts(ctx context.Context, pool *pgxpool.Pool, group []uuid.UUID) {
	small := decimal.RequireFromString(waterfallSmallBal)
	for _, id := range group[:len(group)-1] { // all but the last (cash) account
		pool.Exec(ctx, waterfallSetupSQL, small, id) //nolint:errcheck
	}
}

func runWaterfall(ctx context.Context, pool *pgxpool.Pool, ids []uuid.UUID, concurrency int, dur time.Duration) scenarioResult {
	amt := decimal.RequireFromString(waterfallDebitAmt)

	groups := make([][]uuid.UUID, concurrency)
	for g := range groups {
		start := g * 4
		end := start + 4
		if end > len(ids) {
			end = len(ids)
		}
		groups[g] = ids[start:end]
	}

	fmt.Println("  Initialising account balances...")
	setupWaterfallBalances(ctx, pool, groups)

	drawCounts := make([]int, concurrency) // per-goroutine, no sharing

	col := &collector{}
	runGoroutines(concurrency, dur, func(g int) {
		group := groups[g]
		t0 := time.Now()
		success := waterfallDraw(ctx, pool, group, amt)
		col.record(msElapsed(t0), success, 1) // always 1 transaction per draw
		if success {
			drawCounts[g]++
			if drawCounts[g]%waterfallTopupEvery == 0 {
				topupPriorityAccounts(ctx, pool, group)
			}
		}
	})

	return col.result("Scenario 1 — Waterfall (independent, 4-account groups, no HTTP)", dur)
}

// waterfallDraw tries account[0] optimistically (single CTE, no explicit txn).
// Falls back to a full BEGIN…COMMIT waterfall over accounts[1:] only if needed.
func waterfallDraw(ctx context.Context, pool *pgxpool.Pool, accounts []uuid.UUID, amount decimal.Decimal) bool {
	tag, err := pool.Exec(ctx, waterfallOptimisticSQL, amount, accounts[0])
	if err == nil && tag.RowsAffected() == 1 {
		return true // fast path: 1 statement, no explicit transaction
	}
	if len(accounts) <= 1 {
		return false
	}
	return waterfallOneTxn(ctx, pool, accounts[1:], amount)
}

// waterfallOneTxn runs a full BEGIN…COMMIT waterfall over the given accounts.
func waterfallOneTxn(ctx context.Context, pool *pgxpool.Pool, accounts []uuid.UUID, amount decimal.Decimal) bool {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false
	}
	defer tx.Rollback(ctx)

	for _, id := range accounts {
		var balanceAfter decimal.Decimal
		err := tx.QueryRow(ctx, waterfallDebitSQL, amount, id).Scan(&balanceAfter)
		if err == pgx.ErrNoRows {
			continue // insufficient — try next; transaction stays open
		}
		if err != nil {
			return false
		}
		// Debit succeeded — write ledger entry and commit.
		if _, err := tx.Exec(ctx, waterfallEntrySQL, id, amount, balanceAfter); err != nil {
			return false
		}
		return tx.Commit(ctx) == nil
	}
	return false // all accounts insufficient
}

// ── Scenario 2: Hot account ───────────────────────────────────────────────────

func runHotAccount(ctx context.Context, svc *ledger.Service, ids []uuid.UUID, concurrency int, dur time.Duration) scenarioResult {
	const amount = "0.01"
	amt := decimal.RequireFromString(amount)
	hotID := ids[0]

	// Pre-fund the hot account so it won't drain during the test.
	fmt.Printf("  Pre-funding hot account %s...\n", hotID.String()[:8])
	_, err := svc.Deposit(ctx, ledger.DepositRequest{
		AccountID:      hotID,
		Amount:         decimal.NewFromInt(500_000),
		IdempotencyKey: uuid.New(),
	})
	if err != nil {
		fmt.Printf("  Warning: pre-fund failed (%v)\n", err)
	}

	col := &collector{}
	runGoroutines(concurrency, dur, func(_ int) {
		t0 := time.Now()
		_, err := svc.Withdraw(ctx, ledger.WithdrawRequest{
			AccountID:      hotID,
			Amount:         amt,
			IdempotencyKey: uuid.New(),
		})
		col.record(msElapsed(t0), err == nil, 1)
	})

	return col.result("Scenario 2 — Hot account (all goroutines → 1 account, no HTTP)", dur)
}

// ── Scenario 3: Fan-out ───────────────────────────────────────────────────────

func runFanOut(ctx context.Context, svc *ledger.Service, ids []uuid.UUID, concurrency int, dur time.Duration) scenarioResult {
	const fanOut = 1000
	amt := decimal.RequireFromString("0.001")

	// Last 1000 accounts are shared destinations.
	destStart := len(ids) - fanOut
	if destStart < 0 {
		destStart = 0
	}
	dests := ids[destStart:]
	n := len(dests)

	// Build a template batch; each goroutine copies it to get a fresh set of idempotency keys.
	template := make([]ledger.BatchOp, n)
	for i, id := range dests {
		template[i] = ledger.BatchOp{Type: ledger.BatchOpDeposit, AccountID: id, Amount: amt}
	}

	col := &collector{}
	runGoroutines(concurrency, dur, func(_ int) {
		ops := make([]ledger.BatchOp, n) // BatchOp has no mutable state; copy is safe
		copy(ops, template)

		t0 := time.Now()
		results := svc.ExecBatch(ctx, ops)
		latMs := msElapsed(t0)

		succeeded := 0
		for _, r := range results {
			if r.Success {
				succeeded++
			}
		}
		col.record(latMs, succeeded > 0, succeeded)
	})

	return col.result(
		fmt.Sprintf("Scenario 3 — Fan-out to %d accounts (1 job = %d deposits, no HTTP)", n, n),
		dur,
	)
}

// ── Runner ────────────────────────────────────────────────────────────────────

func runGoroutines(n int, dur time.Duration, fn func(g int)) {
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					fn(g)
				}
			}
		}(g)
	}
	time.Sleep(dur)
	close(stop)
	wg.Wait()
}

// ── Output ────────────────────────────────────────────────────────────────────

func printResults(results []scenarioResult) {
	fmt.Println("\n\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("  Direct-to-DB Benchmark Results")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	for _, r := range results {
		fmt.Printf("\n%s\n", r.name)
		fmt.Printf("  Duration: %.0fs\n\n", r.duration.Seconds())

		p99 := pct(r.lats, 99)
		max := pct(r.lats, 100)

		fmt.Printf("  %-10s %-12s %-12s %-10s %-10s %-8s\n",
			"Jobs", "Jobs/s", "Txns/s", "p99", "max", "Errors")
		fmt.Println("  " + strings.Repeat("─", 62))
		fmt.Printf("  %-10d %-12.0f %-12.0f %-10s %-10s %-8d\n",
			r.jobs, r.jobsPerSec(), r.txnsPerSec(),
			fmtMs(p99), fmtMs(max), r.errors)
	}

	fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func msElapsed(t time.Time) float64 {
	return float64(time.Since(t)) / float64(time.Millisecond)
}

func pct(lats []float64, p float64) float64 {
	if len(lats) == 0 {
		return 0
	}
	s := make([]float64, len(lats))
	copy(s, lats)
	sort.Float64s(s)
	idx := int(math.Ceil(p/100*float64(len(s)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(s) {
		idx = len(s) - 1
	}
	return s[idx]
}

func fmtMs(ms float64) string { return fmt.Sprintf("%.2f ms", ms) }

// toPgUUID is needed for any direct query calls (not used here but kept for reference).
func toPgUUID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }
