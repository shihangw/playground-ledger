// tb.go — TigerBeetle ledger operations.
//
// Each worker exposes two execution paths:
//   doHardcoded — skips PG, uses account IDs known at setup time (pure TB baseline)
//   doWithIDs   — accepts account IDs resolved at runtime by the PG metadata lookup
package main

import (
	"fmt"

	tigerbeetle_go "github.com/tigerbeetle/tigerbeetle-go"
	"github.com/tigerbeetle/tigerbeetle-go/pkg/types"
)

// Account ID ranges (must match IDs stored in user_accounts PG metadata):
//
//   10_000_000 – 10_999_999  waterfall accounts           (7 accts × concurrency)
//                              +0..+3  source accounts A,B,C,D  (DebitsMustNotExceedCredits)
//                              +4      destination X            (no flags)
//                              +5      SETUP control            (no flags)
//                              +6      LIMIT control            (DebitsMustNotExceedCredits)
//   20_000_000               hot source account
//   20_001_000 – 20_001_999  hot dest accounts            (1 per worker)
//   20_002_000               hot funding bank
//   30_000_000 – 30_000_999  fanout source accounts       (1 per worker)
//   31_000_000 – …           fanout dest accounts         (fanoutDests × concurrency)
//   50_000_000               global funding bank

const tbGlobalBank = uint64(50_000_000)

// ── Scenario 1: Waterfall ─────────────────────────────────────────────────────
//
// Implements the TB "Multiple Debits, Single Credit (Balancing Debits)" recipe.
// https://docs.tigerbeetle.com/coding/recipes/multi-debit-credit-transfers/
//
// 7-transfer linked chain for target T and sources A,B,C,D:
//
//   1. SETUP → LIMIT   T           linked
//   2. A     → SETUP   T           linked + balancing_debit + balancing_credit
//   3. B     → SETUP   T           linked + balancing_debit + balancing_credit
//   4. C     → SETUP   T           linked + balancing_debit + balancing_credit
//   5. D     → SETUP   T           linked + balancing_debit + balancing_credit
//   6. SETUP → X       T           linked
//   7. LIMIT → SETUP   AMOUNT_MAX  balancing_credit  (not linked — terminator)
//
// TB drains A first (up to T), then B, C, D in order — server-side, no balance
// pre-read needed. Transfer 6 fails (exceeds_credits on SETUP) if total available
// across A+B+C+D < T, rolling back the entire chain atomically.

const (
	// 1 unit = $0.10. Priority accounts hold $5 (50 units) each;
	// cash holds $100 000 (1 000 000 units). Each priority account serves
	// exactly 50 draws before depleting; top-up fires every 150 draws so the
	// bench cycles A→B→C→D within each top-up period.
	tbWaterfallTarget    = uint64(1)
	tbWaterfallAmountMax = uint64(1_000_000_000_000) // >> any realistic LIMIT balance
)

// waterfallChain holds the 7 account IDs for one independent waterfall chain.
// Priority order: [0] daily credit → [1] monthly credit → [2] bonus credit → [3] cash
type waterfallChain struct {
	sources [4]types.Uint128 // debits_must_not_exceed_credits
	dest    types.Uint128    // X — receives funds
	setup   types.Uint128    // SETUP control — no constraint
	limit   types.Uint128    // LIMIT control — debits_must_not_exceed_credits
}

// tbWaterfallWorker owns nChains independent account sets.
// Each doHardcoded / doWithIDs call submits all chains as one CreateTransfers batch,
// amortising the fixed per-call overhead across nChains transactions.
//
// Source balances: A=B=C=50 units ($5.00), D=1_000_000 ($100 000).
// Each priority account serves exactly 50 draws ($5.00) before depleting.
// maybeTopUp adds +50 to A, B, C every 150 draws, so one full top-up cycle
// covers A (draws 1–50) → B (51–100) → C (101–150) → D (remainder).
const tbTopUpEvery = 150

type tbWaterfallWorker struct {
	chains   []waterfallChain
	drawsCnt int
}

func tbSetupWaterfall(client tigerbeetle_go.Client, nWorkers, nChains int) []tbWaterfallWorker {
	const base = uint64(10_000_000)
	constrained := types.AccountFlags{DebitsMustNotExceedCredits: true}.ToUint16()
	stride := uint64(nChains * 7) // accounts per worker

	accounts := make([]types.Account, 0, 1+nWorkers*nChains*7)
	accounts = append(accounts, types.Account{ID: types.ToUint128(tbGlobalBank), Ledger: 1, Code: 1})
	for i := 0; i < nWorkers; i++ {
		for c := 0; c < nChains; c++ {
			b := base + uint64(i)*stride + uint64(c)*7
			for j := uint64(0); j < 4; j++ { // sources
				accounts = append(accounts, types.Account{ID: types.ToUint128(b + j), Flags: constrained, Ledger: 1, Code: 1})
			}
			accounts = append(accounts, types.Account{ID: types.ToUint128(b + 4), Ledger: 1, Code: 1}) // dest X
			accounts = append(accounts, types.Account{ID: types.ToUint128(b + 5), Ledger: 1, Code: 1}) // SETUP
			accounts = append(accounts, types.Account{ID: types.ToUint128(b + 6), Flags: constrained, Ledger: 1, Code: 1}) // LIMIT
		}
	}
	// CreateAccounts in chunks of 8189 (TB batch limit).
	for len(accounts) > 0 {
		n := 8189
		if n > len(accounts) {
			n = len(accounts)
		}
		tbMustCreateAccounts(client, accounts[:n])
		accounts = accounts[n:]
	}

	// 1 unit = $0.10. Priority accounts: 50 units ($5.00) each; depletes after 50 draws.
	// Cash: 1_000_000 units ($100 000.00) — safety net, never depletes in a cycle.
	sourceFunds := [4]uint64{50, 50, 50, 1_000_000}
	transfers := make([]types.Transfer, 0, nWorkers*nChains*4)
	for i := 0; i < nWorkers; i++ {
		for c := 0; c < nChains; c++ {
			b := base + uint64(i)*stride + uint64(c)*7
			for j := uint64(0); j < 4; j++ {
				transfers = append(transfers, types.Transfer{
					ID: types.ID(), DebitAccountID: types.ToUint128(tbGlobalBank),
					CreditAccountID: types.ToUint128(b + j),
					Amount: types.ToUint128(sourceFunds[j]), Ledger: 1, Code: 1,
				})
			}
		}
	}
	for len(transfers) > 0 {
		n := 8189
		if n > len(transfers) {
			n = len(transfers)
		}
		tbMustCreateTransfers(client, transfers[:n])
		transfers = transfers[n:]
	}

	workers := make([]tbWaterfallWorker, nWorkers)
	for i := 0; i < nWorkers; i++ {
		workers[i].chains = make([]waterfallChain, nChains)
		for c := 0; c < nChains; c++ {
			b := base + uint64(i)*stride + uint64(c)*7
			for j := 0; j < 4; j++ {
				workers[i].chains[c].sources[j] = types.ToUint128(b + uint64(j))
			}
			workers[i].chains[c].dest = types.ToUint128(b + 4)
			workers[i].chains[c].setup = types.ToUint128(b + 5)
			workers[i].chains[c].limit = types.ToUint128(b + 6)
		}
	}
	return workers
}

// maybeTopUp re-funds sources A, B, C (+5 each) for every chain whenever
// drawsCnt reaches tbTopUpEvery. D is left alone — its 10_000 initial balance
// absorbs any remaining draws within the cycle.
func (w *tbWaterfallWorker) maybeTopUp(client tigerbeetle_go.Client) error {
	if w.drawsCnt < tbTopUpEvery {
		return nil
	}
	w.drawsCnt = 0
	tops := make([]types.Transfer, 0, len(w.chains)*3)
	for c := range w.chains {
		ch := &w.chains[c]
		for j := 0; j < 3; j++ {
			tops = append(tops, types.Transfer{
				ID: types.ID(), DebitAccountID: types.ToUint128(tbGlobalBank),
				CreditAccountID: ch.sources[j],
				Amount: types.ToUint128(50), Ledger: 1, Code: 1, // +$5.00 per cycle
			})
		}
	}
	for len(tops) > 0 {
		n := 8189
		if n > len(tops) {
			n = len(tops)
		}
		res, err := client.CreateTransfers(tops[:n])
		if err != nil {
			return err
		}
		if len(res) > 0 {
			return fmt.Errorf("topUp transfer[%d]: %v", res[0].Index, res[0].Result)
		}
		tops = tops[n:]
	}
	return nil
}

// doHardcoded skips PG — submits all chains in one CreateTransfers call.
func (w *tbWaterfallWorker) doHardcoded(client tigerbeetle_go.Client) error {
	if err := w.maybeTopUp(client); err != nil {
		return err
	}
	batch := make([]types.Transfer, 0, len(w.chains)*7)
	for c := range w.chains {
		ch := &w.chains[c]
		batch = appendChain(batch, ch.sources[0], ch.sources[1], ch.sources[2], ch.sources[3], ch.dest, ch.setup, ch.limit)
	}
	if err := tbSubmit(client, batch); err != nil {
		return err
	}
	w.drawsCnt += len(w.chains)
	return nil
}

// doWithIDs submits all chains using source IDs resolved by PG.
// chainIDs[c] is the slice of 4 source account IDs for chain c, ordered by priority.
func (w *tbWaterfallWorker) doWithIDs(client tigerbeetle_go.Client, chainIDs [][]int64) error {
	if err := w.maybeTopUp(client); err != nil {
		return err
	}
	batch := make([]types.Transfer, 0, len(w.chains)*7)
	for c, ids := range chainIDs {
		if len(ids) < 4 {
			return fmt.Errorf("waterfall chain %d: need 4 account IDs, got %d", c, len(ids))
		}
		ch := &w.chains[c]
		a, b, cc, d := types.ToUint128(uint64(ids[0])), types.ToUint128(uint64(ids[1])),
			types.ToUint128(uint64(ids[2])), types.ToUint128(uint64(ids[3]))
		batch = appendChain(batch, a, b, cc, d, ch.dest, ch.setup, ch.limit)
	}
	if err := tbSubmit(client, batch); err != nil {
		return err
	}
	w.drawsCnt += len(chainIDs)
	return nil
}

// appendChain appends the 7-transfer balancing-debit chain for one waterfall to batch.
func appendChain(batch []types.Transfer, a, b, c, d, dest, setup, limit types.Uint128) []types.Transfer {
	T := types.ToUint128(tbWaterfallTarget)
	lnk := types.TransferFlags{Linked: true}.ToUint16()
	bal := types.TransferFlags{Linked: true, BalancingDebit: true, BalancingCredit: true}.ToUint16()
	return append(batch,
		types.Transfer{ID: types.ID(), DebitAccountID: setup, CreditAccountID: limit, Amount: T, Ledger: 1, Code: 1, Flags: lnk},
		types.Transfer{ID: types.ID(), DebitAccountID: a, CreditAccountID: setup, Amount: T, Ledger: 1, Code: 1, Flags: bal},
		types.Transfer{ID: types.ID(), DebitAccountID: b, CreditAccountID: setup, Amount: T, Ledger: 1, Code: 1, Flags: bal},
		types.Transfer{ID: types.ID(), DebitAccountID: c, CreditAccountID: setup, Amount: T, Ledger: 1, Code: 1, Flags: bal},
		types.Transfer{ID: types.ID(), DebitAccountID: d, CreditAccountID: setup, Amount: T, Ledger: 1, Code: 1, Flags: bal},
		types.Transfer{ID: types.ID(), DebitAccountID: setup, CreditAccountID: dest, Amount: T, Ledger: 1, Code: 1, Flags: lnk},
		// T7: NOT linked — cleans up LIMIT regardless of chain outcome
		types.Transfer{ID: types.ID(), DebitAccountID: limit, CreditAccountID: setup,
			Amount: types.ToUint128(tbWaterfallAmountMax), Ledger: 1, Code: 1,
			Flags: types.TransferFlags{BalancingCredit: true}.ToUint16()},
	)
}

// doOptimistic tries a single debit from the highest-priority source (chains[0].sources[0]).
// Because that account has DebitsMustNotExceedCredits, TB returns exceeds_credits if it
// lacks funds — no balance pre-read needed. On success the op completes in 1 transfer and
// 1 TB round-trip. On miss it falls back to the full 7-transfer balancing chain.
// In production the fast path runs ~99% of the time (account is refunded daily).
func (w *tbWaterfallWorker) doOptimistic(client tigerbeetle_go.Client) error {
	if err := w.maybeTopUp(client); err != nil {
		return err
	}
	ch := &w.chains[0]
	res, err := client.CreateTransfers([]types.Transfer{{
		ID:              types.ID(),
		DebitAccountID:  ch.sources[0],
		CreditAccountID: ch.dest,
		Amount:          types.ToUint128(tbWaterfallTarget),
		Ledger:          1, Code: 1,
	}})
	if err != nil {
		return err
	}
	w.drawsCnt++
	if len(res) == 0 {
		return nil // fast path — A had funds
	}
	// Fallback: drain remaining accounts via full balancing chain.
	return tbSubmit(client, appendChain(nil,
		ch.sources[0], ch.sources[1], ch.sources[2], ch.sources[3],
		ch.dest, ch.setup, ch.limit))
}

// doOptimisticWithIDs tries allIDs[0]→dest first; on miss runs the full chain using
// all 4 IDs. allIDs must already be fetched from PG in one round-trip by the caller.
func (w *tbWaterfallWorker) doOptimisticWithIDs(client tigerbeetle_go.Client, allIDs []int64) error {
	if len(allIDs) < 4 {
		return fmt.Errorf("optimistic: need 4 source IDs, got %d", len(allIDs))
	}
	if err := w.maybeTopUp(client); err != nil {
		return err
	}
	ch := &w.chains[0]
	res, err := client.CreateTransfers([]types.Transfer{{
		ID:              types.ID(),
		DebitAccountID:  types.ToUint128(uint64(allIDs[0])),
		CreditAccountID: ch.dest,
		Amount:          types.ToUint128(tbWaterfallTarget),
		Ledger:          1, Code: 1,
	}})
	if err != nil {
		return err
	}
	w.drawsCnt++
	if len(res) == 0 {
		return nil // fast path — primary account had funds
	}
	a := types.ToUint128(uint64(allIDs[0]))
	b := types.ToUint128(uint64(allIDs[1]))
	c := types.ToUint128(uint64(allIDs[2]))
	d := types.ToUint128(uint64(allIDs[3]))
	return tbSubmit(client, appendChain(nil, a, b, c, d, ch.dest, ch.setup, ch.limit))
}

// ── Scenario 2: Hot account withdrawal ───────────────────────────────────────

type tbHotSetup struct {
	hot   types.Uint128   // shared across all goroutines — intentional contention point
	dests []types.Uint128 // one per worker to isolate credit-side
}

func tbSetupHot(client tigerbeetle_go.Client, nWorkers int) *tbHotSetup {
	hot := types.ToUint128(uint64(20_000_000))
	bank := types.ToUint128(uint64(20_002_000))

	accounts := make([]types.Account, 0, 2+nWorkers)
	accounts = append(accounts,
		types.Account{ID: bank, Ledger: 1, Code: 1},
		types.Account{ID: hot, Ledger: 1, Code: 1},
	)
	dests := make([]types.Uint128, nWorkers)
	for i := 0; i < nWorkers; i++ {
		dests[i] = types.ToUint128(uint64(20_001_000 + i))
		accounts = append(accounts, types.Account{ID: dests[i], Ledger: 1, Code: 1})
	}
	tbMustCreateAccounts(client, accounts)

	tbMustCreateTransfers(client, []types.Transfer{{
		ID:              types.ID(),
		DebitAccountID:  bank,
		CreditAccountID: hot,
		Amount:          types.ToUint128(1_000_000_000_000),
		Ledger:          1,
		Code:            1,
	}})
	return &tbHotSetup{hot: hot, dests: dests}
}

// doHardcoded debits s.hot directly — no PG lookup.
func (s *tbHotSetup) doHardcoded(client tigerbeetle_go.Client, workerID int) error {
	return tbSubmit(client, []types.Transfer{{
		ID:              types.ID(),
		DebitAccountID:  s.hot,
		CreditAccountID: s.dests[workerID],
		Amount:          types.ToUint128(1),
		Ledger:          1,
		Code:            1,
	}})
}

// doWithID debits the account ID resolved by PG at runtime.
func (s *tbHotSetup) doWithID(client tigerbeetle_go.Client, hotAccountID int64, workerID int) error {
	return tbSubmit(client, []types.Transfer{{
		ID:              types.ID(),
		DebitAccountID:  types.ToUint128(uint64(hotAccountID)),
		CreditAccountID: s.dests[workerID],
		Amount:          types.ToUint128(1),
		Ledger:          1,
		Code:            1,
	}})
}

// ── Scenario 3: Fan-out ───────────────────────────────────────────────────────

type tbFanoutWorker struct {
	source types.Uint128   // used by doHardcoded
	dests  []types.Uint128 // used by doHardcoded
}

func tbSetupFanout(client tigerbeetle_go.Client, nWorkers, nDests int) []tbFanoutWorker {
	const sourceBase = uint64(30_000_000)
	const destBase = uint64(31_000_000)

	accounts := make([]types.Account, 0, 1+nWorkers*(1+nDests))
	accounts = append(accounts, types.Account{ID: types.ToUint128(tbGlobalBank), Ledger: 1, Code: 1})
	for i := 0; i < nWorkers; i++ {
		accounts = append(accounts, types.Account{
			ID: types.ToUint128(sourceBase + uint64(i)), Ledger: 1, Code: 1,
		})
		for j := 0; j < nDests; j++ {
			accounts = append(accounts, types.Account{
				ID: types.ToUint128(destBase + uint64(i)*uint64(nDests) + uint64(j)), Ledger: 1, Code: 1,
			})
		}
	}
	for len(accounts) > 0 {
		n := 8189
		if n > len(accounts) {
			n = len(accounts)
		}
		tbMustCreateAccounts(client, accounts[:n])
		accounts = accounts[n:]
	}

	fundBatch := make([]types.Transfer, nWorkers)
	for i := 0; i < nWorkers; i++ {
		fundBatch[i] = types.Transfer{
			ID:              types.ID(),
			DebitAccountID:  types.ToUint128(tbGlobalBank),
			CreditAccountID: types.ToUint128(sourceBase + uint64(i)),
			Amount:          types.ToUint128(1_000_000_000),
			Ledger:          1,
			Code:            1,
		}
	}
	tbMustCreateTransfers(client, fundBatch)

	workers := make([]tbFanoutWorker, nWorkers)
	for i := 0; i < nWorkers; i++ {
		workers[i].source = types.ToUint128(sourceBase + uint64(i))
		workers[i].dests = make([]types.Uint128, nDests)
		for j := 0; j < nDests; j++ {
			workers[i].dests[j] = types.ToUint128(destBase + uint64(i)*uint64(nDests) + uint64(j))
		}
	}
	return workers
}

// doHardcoded sends the batch directly using pre-known account IDs.
func (w *tbFanoutWorker) doHardcoded(client tigerbeetle_go.Client) error {
	batch := make([]types.Transfer, len(w.dests))
	for i, dest := range w.dests {
		batch[i] = types.Transfer{
			ID:              types.ID(),
			DebitAccountID:  w.source,
			CreditAccountID: dest,
			Amount:          types.ToUint128(1),
			Ledger:          1,
			Code:            1,
		}
	}
	return tbSubmit(client, batch)
}

// doWithIDs sends the batch using account IDs resolved by PG at runtime.
func (w *tbFanoutWorker) doWithIDs(client tigerbeetle_go.Client, payerID int64, payeeIDs []int64) error {
	batch := make([]types.Transfer, len(payeeIDs))
	for i, destID := range payeeIDs {
		batch[i] = types.Transfer{
			ID:              types.ID(),
			DebitAccountID:  types.ToUint128(uint64(payerID)),
			CreditAccountID: types.ToUint128(uint64(destID)),
			Amount:          types.ToUint128(1),
			Ledger:          1,
			Code:            1,
		}
	}
	return tbSubmit(client, batch)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func tbSubmit(client tigerbeetle_go.Client, batch []types.Transfer) error {
	results, err := client.CreateTransfers(batch)
	if err != nil {
		return err
	}
	if len(results) > 0 {
		return fmt.Errorf("transfer[%d]: %v", results[0].Index, results[0].Result)
	}
	return nil
}

func tbMustCreateAccounts(client tigerbeetle_go.Client, accounts []types.Account) {
	results, err := client.CreateAccounts(accounts)
	if err != nil {
		panic(fmt.Sprintf("CreateAccounts: %v", err))
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
		default:
			panic(fmt.Sprintf("CreateAccounts[%d]: %v", r.Index, r.Result))
		}
	}
}

func tbMustCreateTransfers(client tigerbeetle_go.Client, transfers []types.Transfer) {
	results, err := client.CreateTransfers(transfers)
	if err != nil {
		panic(fmt.Sprintf("CreateTransfers: %v", err))
	}
	for _, r := range results {
		panic(fmt.Sprintf("CreateTransfers[%d]: %v", r.Index, r.Result))
	}
}
