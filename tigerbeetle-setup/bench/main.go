// bench — measures end-to-end latency of the combined production path:
//
//	Cloud SQL (metadata lookup) → TigerBeetle (transfer execution)
//
// Three scenarios:
//
//  1. Waterfall: SELECT 4 account IDs by priority from PG → linked batch in TB
//  2. Hot withdrawal: SELECT 1 account ID from PG → single transfer in TB
//  3. Fan-out: SELECT 1000 payee account IDs from PG → batch 1000 transfers in TB
//
// Usage:
//
//	go run ./bench \
//	  --tb-address 127.0.0.1:3000 \
//	  --pg-dsn "postgres://postgres:PASSWORD@HOST/ledger_bench" \
//	  --duration 15s \
//	  --concurrency 32
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"sync"
	"text/tabwriter"
	"time"

	tigerbeetle_go "github.com/tigerbeetle/tigerbeetle-go"
	"github.com/tigerbeetle/tigerbeetle-go/pkg/types"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	tbAddress      = flag.String("tb-address", "127.0.0.1:3000", "TigerBeetle address")
	tbCluster      = flag.Uint64("tb-cluster", 0, "TigerBeetle cluster ID")
	pgDSN          = flag.String("pg-dsn", "", "PostgreSQL DSN (required)")
	duration       = flag.Duration("duration", 15*time.Second, "Benchmark duration per scenario")
	concurrency    = flag.Int("concurrency", 32, "Parallel goroutines")
	fanoutDests    = flag.Int("fanout-dests", 1000, "Payee accounts for fan-out scenario")
	waterfallBatch = flag.Int("waterfall-batch", 8, "Waterfall chains batched per TB call (amortises round-trip overhead)")
)

type Result struct {
	scenario  string
	variant   string // "TB only" or "PG → TB"
	xfrsPerOp int
	ops       int64
	tps       float64
	xferTPS   float64
	p50    time.Duration
	p99    time.Duration
	pMax   time.Duration
	errors int64
}

func main() {
	flag.Parse()
	if *pgDSN == "" {
		log.Fatal("--pg-dsn is required")
	}

	ctx := context.Background()

	// ── TigerBeetle ───────────────────────────────────────────────────────────
	tbClient, err := tigerbeetle_go.NewClient(types.ToUint128(*tbCluster), []string{*tbAddress})
	if err != nil {
		log.Fatalf("TigerBeetle connect: %v", err)
	}
	defer tbClient.Close()

	// ── PostgreSQL metadata store ──────────────────────────────────────────────
	pgCfg, err := pgxpool.ParseConfig(*pgDSN)
	if err != nil {
		log.Fatalf("PG parse DSN: %v", err)
	}
	pgCfg.MaxConns = int32(*concurrency + 8)
	pgPool, err := pgxpool.NewWithConfig(ctx, pgCfg)
	if err != nil {
		log.Fatalf("PG connect: %v", err)
	}
	defer pgPool.Close()

	if err := pgSetup(ctx, pgPool); err != nil {
		log.Fatalf("PG metadata setup: %v", err)
	}

	var results []Result

	// ── Scenario 1: Waterfall ─────────────────────────────────────────────────
	fmt.Printf("\n=== Scenario 1: Waterfall withdrawal (4-account linked batch, %d chains/call) ===\n", *waterfallBatch)
	tbWF := tbSetupWaterfall(tbClient, *concurrency, *waterfallBatch)
	pgWF := pgSetupWaterfall(ctx, pgPool, *concurrency, *waterfallBatch)

	results = append(results,
		run("1. Waterfall", "TB only", *waterfallBatch, *concurrency, *duration, func(id int) error {
			return tbWF[id].doHardcoded(tbClient)
		}),
		run("1. Waterfall", "PG → TB", *waterfallBatch, *concurrency, *duration, func(id int) error {
			chainIDs, err := pgWF[id].lookupAccounts(ctx, pgPool)
			if err != nil {
				return err
			}
			return tbWF[id].doWithIDs(tbClient, chainIDs)
		}),
		// Optimistic: try highest-priority account first (1 TB transfer); fall back to
		// full 7-transfer balancing chain only if that account is depleted.
		run("1. Waterfall", "Optimistic", 1, *concurrency, *duration, func(id int) error {
			return tbWF[id].doOptimistic(tbClient)
		}),
		// Opt PG→TB: fetch all 4 source IDs in one PG round-trip, then try the fast
		// path with ids[0]. On miss the full chain uses the already-fetched IDs — no
		// second PG round-trip.
		run("1. Waterfall", "Opt PG→TB", 1, *concurrency, *duration, func(id int) error {
			allIDs, err := pgWF[id].lookupFirstChain(ctx, pgPool)
			if err != nil {
				return err
			}
			return tbWF[id].doOptimisticWithIDs(tbClient, allIDs)
		}),
	)

	// ── Scenario 2: Hot account withdrawal ────────────────────────────────────
	fmt.Println("\n=== Scenario 2: Hot account withdrawal (all goroutines, 1 shared source) ===")
	tbHot := tbSetupHot(tbClient, *concurrency)
	pgHot := pgSetupHot(ctx, pgPool)

	results = append(results,
		run("2. Hot withdrawal", "TB only", 1, *concurrency, *duration, func(id int) error {
			return tbHot.doHardcoded(tbClient, id)
		}),
		run("2. Hot withdrawal", "PG → TB", 1, *concurrency, *duration, func(id int) error {
			accountID, err := pgHot.lookupAccount(ctx, pgPool)
			if err != nil {
				return err
			}
			return tbHot.doWithID(tbClient, accountID, id)
		}),
	)

	// ── Scenario 3: Fan-out ───────────────────────────────────────────────────
	fmt.Printf("\n=== Scenario 3: Fan-out (1 source → %d dests per op) ===\n", *fanoutDests)
	tbFO := tbSetupFanout(tbClient, *concurrency, *fanoutDests)
	pgFO := pgSetupFanout(ctx, pgPool, *concurrency, *fanoutDests)

	results = append(results,
		run(fmt.Sprintf("3. Fan-out→%d", *fanoutDests), "TB only", *fanoutDests, *concurrency, *duration, func(id int) error {
			return tbFO[id].doHardcoded(tbClient)
		}),
		run(fmt.Sprintf("3. Fan-out→%d", *fanoutDests), "PG → TB", *fanoutDests, *concurrency, *duration, func(id int) error {
			payerID, err := pgFO[id].lookupPayerAccount(ctx, pgPool)
			if err != nil {
				return err
			}
			payeeIDs, err := pgFO[id].lookupPayeeAccounts(ctx, pgPool)
			if err != nil {
				return err
			}
			return tbFO[id].doWithIDs(tbClient, payerID, payeeIDs)
		}),
	)

	printResults(results)
}

func run(name, variant string, xfrsPerOp, nWorkers int, dur time.Duration, op func(int) error) Result {
	fmt.Printf("  [%s / %s] concurrency=%d duration=%s ...", name, variant, nWorkers, dur)

	type wStats struct {
		lats   []time.Duration
		errors int64
	}
	stats := make([]wStats, nWorkers)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	start := time.Now()
	for i := 0; i < nWorkers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				t0 := time.Now()
				if err := op(id); err != nil {
					stats[id].errors++
				} else {
					stats[id].lats = append(stats[id].lats, time.Since(t0))
				}
			}
		}(i)
	}

	time.Sleep(dur)
	close(stop)
	wg.Wait()
	elapsed := time.Since(start)

	var allLats []time.Duration
	var totalErrs int64
	for _, s := range stats {
		allLats = append(allLats, s.lats...)
		totalErrs += s.errors
	}

	totalOps := int64(len(allLats))
	tps := float64(totalOps) / elapsed.Seconds()
	r := Result{
		scenario:  name,
		variant:   variant,
		xfrsPerOp: xfrsPerOp,
		ops:       totalOps,
		tps:       tps,
		xferTPS:   tps * float64(xfrsPerOp),
		p50:    pct(allLats, 50),
		p99:    pct(allLats, 99),
		pMax:   pct(allLats, 100),
		errors:    totalErrs,
	}
	fmt.Printf(" ops=%d tps=%.0f errors=%d\n", totalOps, tps, totalErrs)
	return r
}

func pct(lats []time.Duration, p float64) time.Duration {
	if len(lats) == 0 {
		return 0
	}
	cp := make([]time.Duration, len(lats))
	copy(cp, lats)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	if p >= 100 {
		return cp[len(cp)-1]
	}
	idx := int(math.Ceil(p/100.0*float64(len(cp)))) - 1
	if idx < 0 {
		idx = 0
	}
	return cp[idx]
}

func printResults(results []Result) {
	fmt.Println()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "Scenario\tVariant\tTxns/s\tp50\tp99\tmax\tErrors\t")
	fmt.Fprintln(w, "--------\t-------\t------\t---\t---\t---\t------\t")
	for _, r := range results {
		// Latency is measured per-call. Divide by xfrsPerOp so the table shows
		// per-transaction latency consistently across all scenarios.
		scale := time.Duration(r.xfrsPerOp)
		if scale < 1 {
			scale = 1
		}
		fmt.Fprintf(w, "%s\t%s\t%.0f\t%s\t%s\t%s\t%d\t\n",
			r.scenario, r.variant,
			r.xferTPS,
			fmtDur(r.p50/scale), fmtDur(r.p99/scale), fmtDur(r.pMax/scale),
			r.errors,
		)
	}
	w.Flush()
}

func fmtDur(d time.Duration) string {
	if d == 0 {
		return "—"
	}
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
}
