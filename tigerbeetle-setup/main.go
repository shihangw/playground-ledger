// verify — connects to a local TigerBeetle instance and runs scenario checks:
//
//  1. Basic:              single transfer, verify balances.
//  2. Two-phase post:     pending → post settlement flow.
//  3. Two-phase void:     pending → void (cancel) flow.
//  4. Waterfall draw:     draw a target amount across multiple source accounts in priority order.
//  5. Linked batch:       atomic chain — one failure rolls back the whole batch.
//  6. Balance constraint: overdraft rejected on a DebitsMustNotExceedCredits account.
//  7. Throughput:         sustained batch transfers, report observed TPS vs 50k target.
//
// Usage:
//
//	go run . [--address 127.0.0.1:3000] [--cluster 0] [--duration 10s] [--concurrency 32] [--batch 8189]
package main

import (
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"

	tigerbeetle_go "github.com/tigerbeetle/tigerbeetle-go"
	"github.com/tigerbeetle/tigerbeetle-go/pkg/types"
)

var (
	address     = flag.String("address", "127.0.0.1:3000", "TigerBeetle address")
	clusterID   = flag.Uint64("cluster", 0, "Cluster ID")
	testDur     = flag.Duration("duration", 10*time.Second, "Throughput test duration")
	concurrency = flag.Int("concurrency", 32, "Parallel goroutines for throughput test")
	batchSize   = flag.Int("batch", 8189, "Transfers per batch (max 8189)")
)

func main() {
	flag.Parse()

	client, err := tigerbeetle_go.NewClient(types.ToUint128(*clusterID), []string{*address})
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer client.Close()

	fmt.Printf("Connected to TigerBeetle at %s (cluster %d)\n\n", *address, *clusterID)

	checkBasic(client)
	checkTwoPhasePost(client)
	checkTwoPhaseVoid(client)
	checkWaterfall(client)
	checkLinkedBatch(client)
	checkBalanceConstraint(client)
	runThroughput(client)
}

// ── 1. Basic transfer ─────────────────────────────────────────────────────────

func checkBasic(client tigerbeetle_go.Client) {
	fmt.Println("── Step 1: basic transfer ──────────────────────────────")

	aliceID := types.ID()
	bobID := types.ID()

	createAccounts(client, []types.Account{
		{ID: aliceID, Ledger: 1, Code: 1},
		{ID: bobID, Ledger: 1, Code: 1},
	})
	fmt.Printf("  alice=%s  bob=%s\n", short(aliceID), short(bobID))

	mustCreateTransfers(client, []types.Transfer{
		{
			ID:              types.ID(),
			DebitAccountID:  aliceID,
			CreditAccountID: bobID,
			Amount:          types.ToUint128(500),
			Ledger:          1,
			Code:            1,
		},
	})

	accounts, _ := client.LookupAccounts([]types.Uint128{aliceID, bobID})
	printBalances(accounts)

	alice := accountByID(accounts, aliceID)
	bob := accountByID(accounts, bobID)
	assertEqual("alice debits_posted", uint128ToUint64(alice.DebitsPosted), 500)
	assertEqual("bob credits_posted", uint128ToUint64(bob.CreditsPosted), 500)
	fmt.Println("  ✓ balances correct")
	fmt.Println()
}

// ── 2. Two-phase: pending → post ─────────────────────────────────────────────

func checkTwoPhasePost(client tigerbeetle_go.Client) {
	fmt.Println("── Step 2: two-phase post ──────────────────────────────")

	payerID, payeeID := types.ID(), types.ID()
	createAccounts(client, []types.Account{
		{ID: payerID, Ledger: 1, Code: 1},
		{ID: payeeID, Ledger: 1, Code: 1},
	})

	pendingID := types.ID()
	mustCreateTransfers(client, []types.Transfer{
		{
			ID:              pendingID,
			DebitAccountID:  payerID,
			CreditAccountID: payeeID,
			Amount:          types.ToUint128(200),
			Ledger:          1,
			Code:            1,
			Flags:           types.TransferFlags{Pending: true}.ToUint16(),
		},
	})
	fmt.Println("  pending: 200 reserved")

	accounts, _ := client.LookupAccounts([]types.Uint128{payerID})
	payer := accountByID(accounts, payerID)
	assertEqual("debits_pending", uint128ToUint64(payer.DebitsPending), 200)
	assertEqual("debits_posted", uint128ToUint64(payer.DebitsPosted), 0)

	mustCreateTransfers(client, []types.Transfer{
		{
			ID:              types.ID(),
			DebitAccountID:  payerID,
			CreditAccountID: payeeID,
			Amount:          types.ToUint128(200),
			PendingID:       pendingID,
			Ledger:          1,
			Code:            1,
			Flags:           types.TransferFlags{PostPendingTransfer: true}.ToUint16(),
		},
	})

	accounts, _ = client.LookupAccounts([]types.Uint128{payerID})
	payer = accountByID(accounts, payerID)
	assertEqual("debits_pending after post", uint128ToUint64(payer.DebitsPending), 0)
	assertEqual("debits_posted after post", uint128ToUint64(payer.DebitsPosted), 200)
	fmt.Println("  ✓ post: pending cleared, posted=200")
	fmt.Println()
}

// ── 3. Two-phase: pending → void (cancel) ────────────────────────────────────

func checkTwoPhaseVoid(client tigerbeetle_go.Client) {
	fmt.Println("── Step 3: two-phase void ──────────────────────────────")

	payerID, payeeID := types.ID(), types.ID()
	createAccounts(client, []types.Account{
		{ID: payerID, Ledger: 1, Code: 1},
		{ID: payeeID, Ledger: 1, Code: 1},
	})

	pendingID := types.ID()
	mustCreateTransfers(client, []types.Transfer{
		{
			ID:              pendingID,
			DebitAccountID:  payerID,
			CreditAccountID: payeeID,
			Amount:          types.ToUint128(150),
			Ledger:          1,
			Code:            1,
			Flags:           types.TransferFlags{Pending: true}.ToUint16(),
		},
	})
	fmt.Println("  pending: 150 reserved")

	accounts, _ := client.LookupAccounts([]types.Uint128{payerID})
	payer := accountByID(accounts, payerID)
	assertEqual("debits_pending before void", uint128ToUint64(payer.DebitsPending), 150)

	// Cancel — void the pending transfer
	mustCreateTransfers(client, []types.Transfer{
		{
			ID:              types.ID(),
			DebitAccountID:  payerID,
			CreditAccountID: payeeID,
			Amount:          types.ToUint128(150),
			PendingID:       pendingID,
			Ledger:          1,
			Code:            1,
			Flags:           types.TransferFlags{VoidPendingTransfer: true}.ToUint16(),
		},
	})

	accounts, _ = client.LookupAccounts([]types.Uint128{payerID})
	payer = accountByID(accounts, payerID)
	assertEqual("debits_pending after void", uint128ToUint64(payer.DebitsPending), 0)
	assertEqual("debits_posted after void", uint128ToUint64(payer.DebitsPosted), 0)
	fmt.Println("  ✓ void: reservation cancelled, no funds moved")
	fmt.Println()
}

// ── 4. Waterfall draw ────────────────────────────────────────────────────────
//
// Draw a target amount from multiple source accounts in priority order.
// Each source is exhausted before drawing from the next.
// The whole draw is submitted as a linked (atomic) batch.

func checkWaterfall(client tigerbeetle_go.Client) {
	fmt.Println("── Step 4: waterfall draw ──────────────────────────────")

	// A "bank" account funds the sources; no balance constraints on it.
	bankID := types.ID()
	srcA := types.ID() // priority 1 — will hold 300
	srcB := types.ID() // priority 2 — will hold 500
	srcC := types.ID() // priority 3 — will hold 700
	destID := types.ID()

	createAccounts(client, []types.Account{
		{ID: bankID, Ledger: 1, Code: 1},
		{ID: srcA, Ledger: 1, Code: 1},
		{ID: srcB, Ledger: 1, Code: 1},
		{ID: srcC, Ledger: 1, Code: 1},
		{ID: destID, Ledger: 1, Code: 1},
	})

	// Fund source accounts from bank
	mustCreateTransfers(client, []types.Transfer{
		{ID: types.ID(), DebitAccountID: bankID, CreditAccountID: srcA, Amount: types.ToUint128(300), Ledger: 1, Code: 1},
		{ID: types.ID(), DebitAccountID: bankID, CreditAccountID: srcB, Amount: types.ToUint128(500), Ledger: 1, Code: 1},
		{ID: types.ID(), DebitAccountID: bankID, CreditAccountID: srcC, Amount: types.ToUint128(700), Ledger: 1, Code: 1},
	})
	fmt.Println("  sources funded: A=300  B=500  C=700")

	// Look up source balances and build waterfall transfers
	sourceIDs := []types.Uint128{srcA, srcB, srcC}
	sources, _ := client.LookupAccounts(sourceIDs)

	const drawTarget = uint64(1200)
	transfers := buildWaterfallTransfers(sources, destID, drawTarget)

	fmt.Printf("  waterfall draw: target=%d\n", drawTarget)
	for _, t := range transfers {
		fmt.Printf("    draw %s from %s\n", uint128Str(t.Amount), short(t.DebitAccountID))
	}

	mustCreateTransfers(client, transfers)

	// Verify destination received the full target
	results, _ := client.LookupAccounts([]types.Uint128{destID, srcA, srcB, srcC})
	dest := accountByID(results, destID)
	assertEqual("dest credits_posted", uint128ToUint64(dest.CreditsPosted), drawTarget)

	// A and B should be exhausted; C should have 300 remaining
	a := accountByID(results, srcA)
	b := accountByID(results, srcB)
	c := accountByID(results, srcC)
	assertEqual("srcA remaining", available(a), 0)
	assertEqual("srcB remaining", available(b), 0)
	assertEqual("srcC remaining", available(c), 300)
	fmt.Printf("  ✓ drew %d: A exhausted, B exhausted, C has 300 remaining\n\n", drawTarget)
}

// buildWaterfallTransfers drains sources in order until target is met.
// All transfers are linked so the draw is atomic.
func buildWaterfallTransfers(sources []types.Account, dest types.Uint128, target uint64) []types.Transfer {
	var transfers []types.Transfer
	remaining := target

	for _, src := range sources {
		if remaining == 0 {
			break
		}
		avail := available(src)
		if avail == 0 {
			continue
		}
		draw := avail
		if draw > remaining {
			draw = remaining
		}
		remaining -= draw
		transfers = append(transfers, types.Transfer{
			ID:              types.ID(),
			DebitAccountID:  src.ID,
			CreditAccountID: dest,
			Amount:          types.ToUint128(draw),
			Ledger:          1,
			Code:            1,
		})
	}

	// Mark all but the last as Linked so the batch is atomic
	for i := 0; i < len(transfers)-1; i++ {
		transfers[i].Flags = types.TransferFlags{Linked: true}.ToUint16()
	}
	return transfers
}

// available returns credits_posted - debits_posted - debits_pending.
func available(a types.Account) uint64 {
	credits := uint128ToUint64(a.CreditsPosted)
	debits := uint128ToUint64(a.DebitsPosted) + uint128ToUint64(a.DebitsPending)
	if debits >= credits {
		return 0
	}
	return credits - debits
}

// ── 5. Linked batch atomicity ────────────────────────────────────────────────
//
// Two transfers are linked. The second one will fail (overdraft on a
// constrained account). TigerBeetle must reject BOTH, leaving no partial state.

func checkLinkedBatch(client tigerbeetle_go.Client) {
	fmt.Println("── Step 5: linked batch atomicity ──────────────────────")

	// goodSrc has 100 credits. badSrc has no credits and DebitsMustNotExceedCredits.
	goodSrc := types.ID()
	badSrc := types.ID()
	dest := types.ID()
	bank := types.ID()

	createAccounts(client, []types.Account{
		{ID: bank, Ledger: 1, Code: 1},
		{ID: goodSrc, Ledger: 1, Code: 1},
		{
			ID:     badSrc,
			Ledger: 1,
			Code:   1,
			Flags:  types.AccountFlags{DebitsMustNotExceedCredits: true}.ToUint16(),
		},
		{ID: dest, Ledger: 1, Code: 1},
	})

	mustCreateTransfers(client, []types.Transfer{
		{ID: types.ID(), DebitAccountID: bank, CreditAccountID: goodSrc, Amount: types.ToUint128(100), Ledger: 1, Code: 1},
	})
	fmt.Println("  goodSrc funded with 100")

	// Linked batch: goodSrc→dest (50, linked) then badSrc→dest (50, will fail)
	results, err := client.CreateTransfers([]types.Transfer{
		{
			ID:              types.ID(),
			DebitAccountID:  goodSrc,
			CreditAccountID: dest,
			Amount:          types.ToUint128(50),
			Ledger:          1,
			Code:            1,
			Flags:           types.TransferFlags{Linked: true}.ToUint16(), // linked to next
		},
		{
			ID:              types.ID(),
			DebitAccountID:  badSrc, // no funds → will fail
			CreditAccountID: dest,
			Amount:          types.ToUint128(50),
			Ledger:          1,
			Code:            1,
		},
	})
	if err != nil {
		log.Fatalf("linked batch: %v", err)
	}
	if len(results) != 2 {
		log.Fatalf("expected 2 failures in linked batch, got %d", len(results))
	}
	fmt.Printf("  transfer[0] result: %v (linked rollback)\n", results[0].Result)
	fmt.Printf("  transfer[1] result: %v (overdraft)\n", results[1].Result)

	// goodSrc must still have its full 100 — the first transfer was rolled back
	accounts, _ := client.LookupAccounts([]types.Uint128{goodSrc, dest})
	gs := accountByID(accounts, goodSrc)
	d := accountByID(accounts, dest)
	assertEqual("goodSrc debits_posted (must be 0, rolled back)", uint128ToUint64(gs.DebitsPosted), 0)
	assertEqual("dest credits_posted (must be 0, nothing settled)", uint128ToUint64(d.CreditsPosted), 0)
	fmt.Println("  ✓ linked rollback: goodSrc intact, dest received nothing")
	fmt.Println()
}

// ── 6. Balance constraint: overdraft rejection ───────────────────────────────

func checkBalanceConstraint(client tigerbeetle_go.Client) {
	fmt.Println("── Step 6: balance constraint ──────────────────────────")

	strictID := types.ID()
	destID := types.ID()

	createAccounts(client, []types.Account{
		{
			ID:     strictID,
			Ledger: 1,
			Code:   1,
			Flags:  types.AccountFlags{DebitsMustNotExceedCredits: true}.ToUint16(),
		},
		{ID: destID, Ledger: 1, Code: 1},
	})
	fmt.Println("  strict account: DebitsMustNotExceedCredits=true, balance=0")

	// Attempt to debit 100 from an account with zero balance → must be rejected
	results, err := client.CreateTransfers([]types.Transfer{
		{
			ID:              types.ID(),
			DebitAccountID:  strictID,
			CreditAccountID: destID,
			Amount:          types.ToUint128(100),
			Ledger:          1,
			Code:            1,
		},
	})
	if err != nil {
		log.Fatalf("balance constraint test: %v", err)
	}
	if len(results) != 1 {
		log.Fatal("expected overdraft to be rejected")
	}
	fmt.Printf("  overdraft result: %v\n", results[0].Result)

	// Verify dest received nothing
	accounts, _ := client.LookupAccounts([]types.Uint128{destID})
	d := accountByID(accounts, destID)
	assertEqual("dest credits_posted (must be 0)", uint128ToUint64(d.CreditsPosted), 0)
	fmt.Println("  ✓ overdraft correctly rejected")
	fmt.Println()
}

// ── 7. Throughput ─────────────────────────────────────────────────────────────

func runThroughput(client tigerbeetle_go.Client) {
	fmt.Printf("── Step 7: throughput (%s, %d goroutines, batch=%d) ────\n",
		*testDur, *concurrency, *batchSize)

	poolSize := *concurrency * 2
	accounts := make([]types.Account, poolSize)
	for i := range accounts {
		accounts[i] = types.Account{
			ID:     types.ToUint128(uint64(9_000_000 + i)),
			Ledger: 1,
			Code:   1,
		}
	}
	createAccounts(client, accounts)
	fmt.Printf("  pre-created %d accounts\n", poolSize)

	var (
		totalTransfers atomic.Int64
		totalErrors    atomic.Int64
		wg             sync.WaitGroup
		stop           = make(chan struct{})
	)

	start := time.Now()

	for g := 0; g < *concurrency; g++ {
		debitID := types.ToUint128(uint64(9_000_000 + g*2))
		creditID := types.ToUint128(uint64(9_000_000 + g*2 + 1))

		wg.Add(1)
		go func(debit, credit types.Uint128) {
			defer wg.Done()
			batch := make([]types.Transfer, *batchSize)
			for {
				select {
				case <-stop:
					return
				default:
				}
				for i := range batch {
					batch[i] = types.Transfer{
						ID:              types.ID(),
						DebitAccountID:  debit,
						CreditAccountID: credit,
						Amount:          types.ToUint128(1),
						Ledger:          1,
						Code:            1,
					}
				}
				results, err := client.CreateTransfers(batch)
				if err != nil {
					if totalErrors.Add(1) == 1 {
						log.Printf("throughput error (first): %v", err)
					}
					continue
				}
				sent := int64(*batchSize) - int64(len(results))
				totalTransfers.Add(sent)
			}
		}(debitID, creditID)
	}

	time.Sleep(*testDur)
	close(stop)
	wg.Wait()

	elapsed := time.Since(start)
	total := totalTransfers.Load()
	errs := totalErrors.Load()
	tps := float64(total) / elapsed.Seconds()

	fmt.Printf("\n  Duration:     %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("  Transfers:    %d\n", total)
	fmt.Printf("  Errors:       %d\n", errs)
	fmt.Printf("  Observed TPS: %.0f\n", tps)

	const target = 50_000
	if tps >= target {
		fmt.Printf("  ✓ target of %d TPS met\n", target)
	} else {
		fmt.Printf("  ✗ below target of %d TPS (%.1f%%)\n", target, tps/target*100)
		fmt.Println("    → try increasing --concurrency or --batch, or check TB server resources")
	}
	fmt.Println()
}

// ── helpers ───────────────────────────────────────────────────────────────────

func createAccounts(client tigerbeetle_go.Client, accounts []types.Account) {
	results, err := client.CreateAccounts(accounts)
	if err != nil {
		log.Fatalf("CreateAccounts: %v", err)
	}
	for _, r := range results {
		switch r.Result {
		case types.AccountExists,
			types.AccountExistsWithDifferentFlags,
			types.AccountExistsWithDifferentUserData128,
			types.AccountExistsWithDifferentUserData64,
			types.AccountExistsWithDifferentUserData32,
			types.AccountExistsWithDifferentLedger,
			types.AccountExistsWithDifferentCode:
			// idempotent — already exists from a prior run
		default:
			log.Fatalf("CreateAccounts[%d]: %v", r.Index, r.Result)
		}
	}
}

func mustCreateTransfers(client tigerbeetle_go.Client, transfers []types.Transfer) {
	results, err := client.CreateTransfers(transfers)
	if err != nil {
		log.Fatalf("CreateTransfers: %v", err)
	}
	for _, r := range results {
		log.Fatalf("CreateTransfers[%d]: %v", r.Index, r.Result)
	}
}

func printBalances(accounts []types.Account) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  account\tdebits_posted\tcredits_posted\t")
	for _, a := range accounts {
		fmt.Fprintf(w, "  %s\t%s\t%s\t\n", short(a.ID), uint128Str(a.DebitsPosted), uint128Str(a.CreditsPosted))
	}
	w.Flush()
}

func accountByID(accounts []types.Account, id types.Uint128) types.Account {
	for _, a := range accounts {
		if a.ID == id {
			return a
		}
	}
	log.Fatalf("account %s not found", id.String())
	return types.Account{}
}

func assertEqual(label string, got, want uint64) {
	if got != want {
		log.Fatalf("assertion failed — %s: got %d, want %d", label, got, want)
	}
}

func uint128ToUint64(v types.Uint128) uint64 {
	b := v.BigInt()
	if !b.IsUint64() {
		log.Fatalf("uint128 value overflows uint64: %s", b.String())
	}
	return b.Uint64()
}

func uint128Str(v types.Uint128) string {
	b := v.BigInt()
	if b.Cmp(big.NewInt(0)) == 0 {
		return "0"
	}
	return b.String()
}

// short returns the last 7 hex chars of a Uint128 for readable output.
func short(id types.Uint128) string {
	s := id.String()
	if len(s) > 7 {
		return "…" + s[len(s)-7:]
	}
	return s
}
