package main

import (
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"time"
)

func cmdVerify(args []string) {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	apiURL := fs.String("api-url", "http://localhost:8080", "API base URL")
	opsPerAccount := fs.Int("ops-per-account", 5, "Deposits and withdrawals per account for scenario 1")
	transferCount := fs.Int("transfer-count", 500, "Number of random transfers for scenario 2")
	amount := fs.String("amount", "1", "Amount per operation")
	fs.Parse(args)

	client := NewLedgerClient(*apiURL)
	fmt.Println("Ledger correctness tests")
	fmt.Printf("API: %s\n\n", *apiURL)

	fmt.Println("Loading accounts...")
	allAccounts, err := client.GetAllUsers(2000)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading accounts: %v\n", err)
		os.Exit(1)
	}
	if len(allAccounts) < 10 {
		fmt.Fprintf(os.Stderr, "Need at least 10 accounts. Got %d. Run: ledger-stress seed --count 1000\n", len(allAccounts))
		os.Exit(1)
	}
	pool := allAccounts
	if len(pool) > 1000 {
		pool = pool[:1000]
	}
	fmt.Printf("Using %d accounts\n\n", len(pool))

	allPass := true

	// Scenario 1
	fmt.Println("━━━ Scenario 1: Deposit + Withdraw Balance Invariant ━━━")
	fmt.Println("  For each account: send N deposits and N withdrawals concurrently.")
	fmt.Println("  Invariant: final_balance = initial_balance (when all ops succeed).\n")
	s1start := time.Now()
	s1pass, s1detail := scenario1(client, pool, *opsPerAccount, *amount)
	fmt.Println(s1detail)
	if s1pass {
		fmt.Printf("  ✓ PASSED  (%.1fs)\n\n", time.Since(s1start).Seconds())
	} else {
		fmt.Printf("  ✗ FAILED  (%.1fs)\n\n", time.Since(s1start).Seconds())
		allPass = false
	}

	// Reload balances for scenario 2
	fmt.Println("Reloading balances for scenario 2...")
	refreshed, err := client.GetAllUsers(2000)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reloading accounts: %v\n", err)
		os.Exit(1)
	}
	poolIDs := make(map[string]bool, len(pool))
	for _, a := range pool {
		poolIDs[a.ID] = true
	}
	var poolRefreshed []AccountInfo
	for _, a := range refreshed {
		if poolIDs[a.ID] {
			poolRefreshed = append(poolRefreshed, a)
		}
	}

	// Scenario 2
	fmt.Println("━━━ Scenario 2: Transfer Sum Invariant ━━━")
	fmt.Println("  Random transfers between accounts.")
	fmt.Println("  Invariant: sum(all balances) is unchanged before and after.\n")
	s2start := time.Now()
	s2pass, s2detail := scenario2(client, poolRefreshed, *transferCount, *amount)
	fmt.Println(s2detail)
	if s2pass {
		fmt.Printf("  ✓ PASSED  (%.1fs)\n\n", time.Since(s2start).Seconds())
	} else {
		fmt.Printf("  ✗ FAILED  (%.1fs)\n\n", time.Since(s2start).Seconds())
		allPass = false
	}

	// Scenario 3
	fmt.Println("━━━ Scenario 3: Ledger Reconciliation ━━━")
	fmt.Println("  SQL: accounts.balance = SUM(CREDIT entries) − SUM(DEBIT entries)")
	fmt.Println("  Checks every account in the database.\n")
	s3start := time.Now()
	s3pass, s3detail := scenario3(client)
	fmt.Println(s3detail)
	if s3pass {
		fmt.Printf("  ✓ PASSED  (%.1fs)\n\n", time.Since(s3start).Seconds())
	} else {
		fmt.Printf("  ✗ FAILED  (%.1fs)\n\n", time.Since(s3start).Seconds())
		allPass = false
	}

	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	if allPass {
		fmt.Println("Overall: ✓ ALL PASSED")
		os.Exit(0)
	} else {
		fmt.Println("Overall: ✗ SOME FAILED")
		os.Exit(1)
	}
}

// Decimal arithmetic using integer representation at 6 decimal places.
const decimalScale = int64(1_000_000)

func parseDecimal(s string) int64 {
	f, _ := strconv.ParseFloat(s, 64)
	return int64(math.Round(f * float64(decimalScale)))
}

func formatDecimal(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	whole := n / decimalScale
	frac := n % decimalScale
	s := fmt.Sprintf("%d.%06d", whole, frac)
	if neg {
		s = "-" + s
	}
	return s
}

// scenario1: for each account, run N concurrent deposits and N concurrent
// withdrawals of the same amount. The final balance must equal the initial
// balance adjusted for any ops that succeeded or failed.
func scenario1(client *LedgerClient, accounts []AccountInfo, opsPerAccount int, amount string) (bool, string) {
	n := len(accounts)
	initial := make(map[string]int64, n)
	for _, a := range accounts {
		initial[a.ID] = parseDecimal(a.Balance)
	}
	netOps := make(map[string]int, n)

	type indOp struct {
		typ string
		acc AccountInfo
	}
	var allOps []indOp
	for i := 0; i < opsPerAccount; i++ {
		for _, acc := range accounts {
			allOps = append(allOps, indOp{"deposit", acc})
			allOps = append(allOps, indOp{"withdraw", acc})
		}
	}
	rand.Shuffle(len(allOps), func(i, j int) { allOps[i], allOps[j] = allOps[j], allOps[i] })

	const concurrency = 50
	var mu sync.Mutex
	depositOk, withdrawOk, errors := 0, 0, 0

	for i := 0; i < len(allOps); i += concurrency {
		end := i + concurrency
		if end > len(allOps) {
			end = len(allOps)
		}
		chunk := allOps[i:end]

		type res struct {
			typ     string
			accID   string
			success bool
		}
		results := make([]res, len(chunk))
		var wg sync.WaitGroup
		for k, op := range chunk {
			wg.Add(1)
			k, op := k, op
			go func() {
				defer wg.Done()
				token := fmt.Sprintf("dev_%s", op.acc.UserID)
				var r StressResult
				if op.typ == "deposit" {
					r = client.Deposit(op.acc.ID, amount, token)
				} else {
					r = client.Withdraw(op.acc.ID, amount, token)
				}
				results[k] = res{op.typ, op.acc.ID, r.Success}
			}()
		}
		wg.Wait()

		mu.Lock()
		for _, r := range results {
			if r.success {
				if r.typ == "deposit" {
					netOps[r.accID]++
					depositOk++
				} else {
					netOps[r.accID]--
					withdrawOk++
				}
			} else {
				errors++
			}
		}
		mu.Unlock()
	}

	afterAccs, err := client.GetAllUsers(2000)
	if err != nil {
		return false, fmt.Sprintf("  error fetching final balances: %v", err)
	}
	finalBal := make(map[string]int64, len(afterAccs))
	for _, a := range afterAccs {
		finalBal[a.ID] = parseDecimal(a.Balance)
	}

	amountInt := parseDecimal(amount)
	failures := 0
	var lines []string

	for _, acc := range accounts {
		init := initial[acc.ID]
		net := int64(netOps[acc.ID])
		expected := init + net*amountInt
		actual, exists := finalBal[acc.ID]
		if !exists {
			actual = init
		}
		if expected != actual {
			failures++
			if len(lines) < 10 {
				lines = append(lines, fmt.Sprintf("  ✗ %s... expected=%s actual=%s diff=%s",
					acc.ID[:8], formatDecimal(expected), formatDecimal(actual), formatDecimal(actual-expected)))
			}
		}
	}

	detail := fmt.Sprintf("  accounts=%d  ops/account=%d  amount=$%s\n  deposits_ok=%d  withdrawals_ok=%d  errors=%d\n  balance_failures=%d",
		n, opsPerAccount, amount, depositOk, withdrawOk, errors, failures)
	for _, l := range lines {
		detail += "\n" + l
	}
	return failures == 0, detail
}

// scenario2: run random peer-to-peer transfers between accounts. The total
// sum of all balances must be identical before and after.
func scenario2(client *LedgerClient, accounts []AccountInfo, transferCount int, amount string) (bool, string) {
	n := len(accounts)
	var initialSum int64
	for _, a := range accounts {
		initialSum += parseDecimal(a.Balance)
	}

	type transfer struct{ src, dst AccountInfo }
	transfers := make([]transfer, transferCount)
	for i := range transfers {
		srcIdx := rand.Intn(n)
		dstIdx := rand.Intn(n - 1)
		if dstIdx >= srcIdx {
			dstIdx++
		}
		transfers[i] = transfer{accounts[srcIdx], accounts[dstIdx]}
	}

	const concurrency = 50
	var mu sync.Mutex
	ok, failed := 0, 0

	for i := 0; i < len(transfers); i += concurrency {
		end := i + concurrency
		if end > len(transfers) {
			end = len(transfers)
		}
		chunk := transfers[i:end]
		results := make([]bool, len(chunk))
		var wg sync.WaitGroup
		for k, t := range chunk {
			wg.Add(1)
			k, t := k, t
			go func() {
				defer wg.Done()
				r := client.Transfer(t.src.ID, t.dst.ID, amount, fmt.Sprintf("dev_%s", t.src.UserID))
				results[k] = r.Success
			}()
		}
		wg.Wait()
		mu.Lock()
		for _, success := range results {
			if success {
				ok++
			} else {
				failed++
			}
		}
		mu.Unlock()
	}

	afterAccs, err := client.GetAllUsers(2000)
	if err != nil {
		return false, fmt.Sprintf("  error fetching final balances: %v", err)
	}
	afterMap := make(map[string]int64, len(afterAccs))
	for _, a := range afterAccs {
		afterMap[a.ID] = parseDecimal(a.Balance)
	}
	var finalSum int64
	for _, acc := range accounts {
		if v, exists := afterMap[acc.ID]; exists {
			finalSum += v
		} else {
			finalSum += parseDecimal(acc.Balance)
		}
	}

	diff := finalSum - initialSum
	detail := fmt.Sprintf("  accounts=%d  transfers=%d  amount=$%s\n  transfers_ok=%d  transfers_failed=%d\n  initial_sum=%s  final_sum=%s  diff=%s",
		n, transferCount, amount, ok, failed, formatDecimal(initialSum), formatDecimal(finalSum), formatDecimal(diff))
	return diff == 0, detail
}

// scenario3: server-side SQL reconciliation check — accounts.balance must
// equal SUM(CREDIT entries) − SUM(DEBIT entries) for every account.
func scenario3(client *LedgerClient) (bool, string) {
	rec, err := client.Reconcile(0)
	if err != nil {
		return false, fmt.Sprintf("  error: %v", err)
	}
	detail := fmt.Sprintf("  discrepancies=%d", rec.DiscrepancyCount)
	for i, d := range rec.Discrepancies {
		if i >= 10 {
			break
		}
		idPrefix := d.AccountID
		if len(idPrefix) > 8 {
			idPrefix = idPrefix[:8]
		}
		detail += fmt.Sprintf("\n  ✗ %s... balance=%s ledger=%s diff=%s entries=%d",
			idPrefix, d.AccountBalance, d.LedgerBalance, d.Discrepancy, d.EntryCount)
	}
	return rec.OK, detail
}
