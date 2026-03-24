/**
 * Correctness test suite for the ledger.
 *
 * Three scenarios, each operating over ~1000 accounts:
 *
 * Scenario 1 – Deposit+Withdraw Balance Invariant
 *   For every account in the pool: send N concurrent deposits and N concurrent
 *   withdrawals of the same amount, interleaved (creating real row-level lock
 *   contention). Because the counts are symmetric, a fully-correct ledger must
 *   end up at the starting balance for every account. Any lost update, double
 *   application, or balance/ledger divergence surfaces as a failure here.
 *
 * Scenario 2 – Transfer Sum Invariant
 *   Record the total balance across 1000 accounts. Run many random peer-to-peer
 *   transfers between them. After all transfers settle, re-sum the balances.
 *   The total must be identical — transfers must be zero-sum. A bug in the
 *   double-entry logic (e.g., crediting without debiting) shows up here.
 *
 * Scenario 3 – Ledger Reconciliation
 *   Server-side SQL check: for every account in the DB,
 *     accounts.balance  =  SUM(CREDIT entries) − SUM(DEBIT entries)
 *   This catches any divergence between the running balance and the immutable
 *   audit trail, regardless of which code path created the entries.
 */

import { LedgerClient } from "../lib/api-client.js";

// ─── helpers ────────────────────────────────────────────────────────────────

/** Precise decimal arithmetic using integer cents (6 decimal places). */
const SCALE = 1_000_000n;

function toInt(s: string): bigint {
  // round to 6dp to avoid float noise
  return BigInt(Math.round(parseFloat(s) * 1_000_000));
}

function pad(n: number, w = 10): string {
  return String(n).padStart(w);
}

function fmt(n: bigint): string {
  const neg = n < 0n;
  const abs = neg ? -n : n;
  const whole = abs / SCALE;
  const frac = abs % SCALE;
  return `${neg ? "-" : ""}${whole}.${String(frac).padStart(6, "0")}`;
}

type Account = { id: string; user_id: string; balance: string };

// ─── scenario 1: deposit + withdraw → balance invariant ─────────────────────

/**
 * Sends N deposits and N withdrawals of $amount to every account in `pool`
 * concurrently via individual deposit/withdraw endpoints (not the batch
 * endpoint). Using individual requests gives unambiguous per-op success/failure
 * — each HTTP response directly reflects whether the DB applied the op. There
 * is no batch-level error-propagation to reason about.
 *
 * All ops are interleaved and shuffled before dispatch so that deposits and
 * withdrawals race on the same rows (real row-level lock contention). Some ops
 * may fail with lock-timeout/insufficient-funds; those are accounted for in the
 * expected balance.
 *
 * Correctness check: for each account,
 *   final_balance = initial_balance + (deposits_ok − withdrawals_ok) × amount
 *
 * When all operations succeed (expected with ample initial balance):
 *   final_balance = initial_balance
 */
async function scenario1(
  client: LedgerClient,
  accounts: Account[],
  opsPerAccount: number,
  amount: string,
): Promise<{ pass: boolean; detail: string }> {
  const n = accounts.length;
  const initial = new Map(accounts.map((a) => [a.id, toInt(a.balance)]));

  // Track per-account net expected change (deposits - withdrawals) × amount
  const netOps = new Map<string, number>(accounts.map((a) => [a.id, 0]));

  // Build individual ops for every account, then shuffle to maximise
  // interleaving between deposits and withdrawals on the same rows.
  type IndividualOp = { type: "deposit" | "withdraw"; acc: Account };
  const allOps: IndividualOp[] = [];
  for (let i = 0; i < opsPerAccount; i++) {
    for (const acc of accounts) {
      allOps.push({ type: "deposit", acc });
      allOps.push({ type: "withdraw", acc });
    }
  }
  for (let i = allOps.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [allOps[i], allOps[j]] = [allOps[j], allOps[i]];
  }

  // Dispatch all ops concurrently (max 50 in-flight). Individual endpoints
  // return unambiguous success/failure per-op — no batch poisoning to worry
  // about — so netOps tracking is always accurate.
  const CONCURRENCY = 50;
  let depositOk = 0, withdrawOk = 0, errors = 0;

  for (let i = 0; i < allOps.length; i += CONCURRENCY) {
    const chunk = allOps.slice(i, i + CONCURRENCY);
    const results = await Promise.all(
      chunk.map(({ type, acc }) => {
        const token = `dev_${acc.user_id}`;
        return type === "deposit"
          ? client.deposit(acc.id, amount, token)
          : client.withdraw(acc.id, amount, token);
      })
    );
    for (let k = 0; k < chunk.length; k++) {
      const { type, acc } = chunk[k];
      const r = results[k];
      if (r.success) {
        const delta = type === "deposit" ? 1 : -1;
        netOps.set(acc.id, (netOps.get(acc.id) ?? 0) + delta);
        if (type === "deposit") depositOk++; else withdrawOk++;
      } else {
        errors++;
      }
    }
  }

  // Fetch final balances — use the same large limit as the initial load to
  // avoid non-deterministic ordering dropping pool accounts out of the window.
  const afterResp = await client.getAllUsers(2000);
  const finalBal = new Map(afterResp.map((a) => [a.id, toInt(a.balance)]));

  const amountInt = toInt(amount);
  let failures = 0;
  const lines: string[] = [];

  for (const acc of accounts) {
    const init = initial.get(acc.id)!;
    const net = BigInt(netOps.get(acc.id) ?? 0);
    const expected = init + net * amountInt;
    const actual = finalBal.get(acc.id) ?? init;
    if (expected !== actual) {
      failures++;
      lines.push(
        `  ✗ ${acc.id.slice(0, 8)}... expected=${fmt(expected)} actual=${fmt(actual)} diff=${fmt(actual - expected)}`
      );
    }
  }

  const detail = [
    `  accounts=${n}  ops/account=${opsPerAccount}  amount=$${amount}`,
    `  deposits_ok=${depositOk}  withdrawals_ok=${withdrawOk}  errors=${errors}`,
    `  balance_failures=${failures}`,
    ...lines.slice(0, 10),
  ].join("\n");

  return { pass: failures === 0, detail };
}

// ─── scenario 2: transfer sum invariant ─────────────────────────────────────

/**
 * Executes `transferCount` random peer-to-peer transfers of $amount between
 * accounts in `pool`. All transfers run concurrently (max 50 in-flight).
 *
 * Correctness check: sum of all account balances must be identical before and
 * after. Any bug that creates or destroys money — e.g., crediting without
 * debiting, or debiting twice — changes the sum.
 *
 * Note: some transfers may fail (contention / lock timeout). That is fine
 * because a failed transfer is a no-op: the sum is still preserved. Only a
 * partially-applied transfer (debit without credit, or vice versa) would
 * change the sum.
 */
async function scenario2(
  client: LedgerClient,
  accounts: Account[],
  transferCount: number,
  amount: string,
): Promise<{ pass: boolean; detail: string }> {
  const n = accounts.length;
  const initialSum = accounts.reduce((s, a) => s + toInt(a.balance), 0n);

  // Generate random transfer pairs (src ≠ dst)
  type Transfer = { src: Account; dst: Account };
  const transfers: Transfer[] = [];
  for (let i = 0; i < transferCount; i++) {
    let srcIdx = Math.floor(Math.random() * n);
    let dstIdx = Math.floor(Math.random() * (n - 1));
    if (dstIdx >= srcIdx) dstIdx++;
    transfers.push({ src: accounts[srcIdx], dst: accounts[dstIdx] });
  }

  // Execute transfers concurrently (max 50 in-flight)
  const CONCURRENCY = 50;
  let ok = 0, failed = 0;

  for (let i = 0; i < transfers.length; i += CONCURRENCY) {
    const chunk = transfers.slice(i, i + CONCURRENCY);
    const results = await Promise.all(
      chunk.map(({ src, dst }) =>
        client.transfer(src.id, dst.id, amount, `dev_${src.user_id}`)
      )
    );
    for (const r of results) {
      if (r.success) ok++; else failed++;
    }
  }

  // Fetch final balances and re-sum
  const afterResp = await client.getAllUsers(2000);
  const afterMap = new Map(afterResp.map((a) => [a.id, toInt(a.balance)]));
  const finalSum = accounts.reduce((s, a) => s + (afterMap.get(a.id) ?? toInt(a.balance)), 0n);

  const diff = finalSum - initialSum;
  const pass = diff === 0n;

  const detail = [
    `  accounts=${n}  transfers=${transferCount}  amount=$${amount}`,
    `  transfers_ok=${ok}  transfers_failed=${failed}`,
    `  initial_sum=${fmt(initialSum)}  final_sum=${fmt(finalSum)}  diff=${fmt(diff)}`,
  ].join("\n");

  return { pass, detail };
}

// ─── scenario 3: ledger reconciliation ──────────────────────────────────────

async function scenario3(
  client: LedgerClient,
): Promise<{ pass: boolean; detail: string }> {
  const rec = await client.reconcile();
  const lines: string[] = [
    `  discrepancies=${rec.discrepancy_count}`,
  ];
  for (const d of rec.discrepancies.slice(0, 10)) {
    lines.push(
      `  ✗ ${d.account_id.slice(0, 8)}... balance=${d.account_balance} ledger=${d.ledger_balance} diff=${d.discrepancy} entries=${d.entry_count}`
    );
  }
  return { pass: rec.ok, detail: lines.join("\n") };
}

// ─── main ────────────────────────────────────────────────────────────────────

export async function verifyCommand(opts: {
  apiUrl: string;
  opsPerAccount: number;
  transferCount: number;
  amount: string;
}) {
  const client = new LedgerClient(opts.apiUrl);

  console.log(`Ledger correctness tests`);
  console.log(`API: ${opts.apiUrl}\n`);

  // Load the full account pool (up to 2000)
  console.log("Loading accounts...");
  const allAccounts = await client.getAllUsers(2000);
  if (allAccounts.length < 10) {
    console.error(`Need at least 10 accounts. Got ${allAccounts.length}. Run: ledger-stress seed --count 1000`);
    process.exit(1);
  }
  // Use up to 1000 accounts for each scenario
  const pool = allAccounts.slice(0, Math.min(1000, allAccounts.length));
  console.log(`Using ${pool.length} accounts\n`);

  let allPass = true;

  // ── Scenario 1 ──────────────────────────────────────────────────────────
  console.log("━━━ Scenario 1: Deposit + Withdraw Balance Invariant ━━━");
  console.log("  For each account: send N deposits and N withdrawals concurrently.");
  console.log("  Invariant: final_balance = initial_balance (when all ops succeed).\n");
  const s1start = performance.now();
  const s1 = await scenario1(client, pool, opts.opsPerAccount, opts.amount);
  const s1elapsed = ((performance.now() - s1start) / 1000).toFixed(1);
  console.log(s1.detail);
  console.log(`  ${s1.pass ? "✓ PASSED" : "✗ FAILED"}  (${s1elapsed}s)\n`);
  if (!s1.pass) allPass = false;

  // Reload balances after scenario 1 for scenario 2
  console.log("Reloading balances for scenario 2...");
  const refreshed = await client.getAllUsers(2000);
  const poolRefreshed = refreshed.filter((a) => pool.some((p) => p.id === a.id));

  // ── Scenario 2 ──────────────────────────────────────────────────────────
  console.log("━━━ Scenario 2: Transfer Sum Invariant ━━━");
  console.log("  Random transfers between accounts.");
  console.log("  Invariant: sum(all balances) is unchanged before and after.\n");
  const s2start = performance.now();
  const s2 = await scenario2(client, poolRefreshed, opts.transferCount, opts.amount);
  const s2elapsed = ((performance.now() - s2start) / 1000).toFixed(1);
  console.log(s2.detail);
  console.log(`  ${s2.pass ? "✓ PASSED" : "✗ FAILED"}  (${s2elapsed}s)\n`);
  if (!s2.pass) allPass = false;

  // ── Scenario 3 ──────────────────────────────────────────────────────────
  console.log("━━━ Scenario 3: Ledger Reconciliation ━━━");
  console.log("  SQL: accounts.balance = SUM(CREDIT entries) − SUM(DEBIT entries)");
  console.log("  Checks every account in the database.\n");
  const s3start = performance.now();
  const s3 = await scenario3(client);
  const s3elapsed = ((performance.now() - s3start) / 1000).toFixed(1);
  console.log(s3.detail);
  console.log(`  ${s3.pass ? "✓ PASSED" : "✗ FAILED"}  (${s3elapsed}s)\n`);
  if (!s3.pass) allPass = false;

  // ── Summary ─────────────────────────────────────────────────────────────
  console.log("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━");
  console.log(`Overall: ${allPass ? "✓ ALL PASSED" : "✗ SOME FAILED"}`);
  process.exit(allPass ? 0 : 1);
}
