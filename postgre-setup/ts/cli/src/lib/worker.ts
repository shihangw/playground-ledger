// Worker process for stress testing.
// Spawned by the stress command to run requests in parallel across processes.
// Communicates results back to parent via IPC messages.

import { LedgerClient } from "./api-client.js";
import { AccountInfo, BatchOp } from "./types.js";
import fs from "node:fs";

interface WorkerConfig {
  apiUrl: string;
  mode: string;
  accountsFile: string;
  rate: number; // per-worker RPS
  concurrency: number; // per-worker concurrency
  duration: number; // seconds
  runId: string;
  workerId: number;
  batchSize: number; // 0 = single requests, >0 = batch N ops per HTTP call
}

interface WorkerResult {
  type: "result";
  success: boolean;
  latencyMs: number;
  errorType?: string;
  eventType: string;
  accountId: string;
}

interface WorkerDone {
  type: "done";
  workerId: number;
  totalRequests: number;
}

const config: WorkerConfig = JSON.parse(process.argv[2]);
const client = new LedgerClient(config.apiUrl);
const accounts: AccountInfo[] = JSON.parse(fs.readFileSync(config.accountsFile, "utf8"));

function pickRandom(): AccountInfo {
  return accounts[Math.floor(Math.random() * accounts.length)];
}

function pickTaskType(): "deposit" | "withdraw" | "grant-issue" | "grant-drawdown" {
  switch (config.mode) {
    case "deposits": return "deposit";
    case "withdrawals": return "withdraw";
    case "mixed": return Math.random() < 0.3 ? "deposit" : "withdraw";
    case "credit-grants": return "grant-issue";
    case "credit-drawdown": return "grant-drawdown";
    case "credit-mixed": return Math.random() < 0.3 ? "grant-issue" : "grant-drawdown";
    default: return "deposit";
  }
}

function makeBatchOp(): BatchOp {
  const account = pickRandom();
  const taskType = pickTaskType();
  const op = (taskType === "deposit" || taskType === "grant-issue") ? "deposit" : "withdraw";
  const amount = op === "deposit"
    ? (Math.random() * 490 + 10).toFixed(2)
    : (Math.random() * 0.99 + 0.01).toFixed(2);
  return { op, account_id: account.id, amount };
}

function classifyBatchError(error?: string): WorkerResult["errorType"] {
  if (!error) return "other";
  if (error.includes("insufficient grant")) return "insufficient_grants";
  if (error.includes("insufficient funds")) return "insufficient_funds";
  if (error.includes("contention") || error.includes("deadlock") || error.includes("lock timeout")) return "contention";
  return "other";
}

async function runTask() {
  process.send!({ type: "http_req" });
  const account = pickRandom();
  const taskType = pickTaskType();
  const userToken = `dev_${account.user_id}`;
  let eventType: string;
  let result;

  switch (taskType) {
    case "deposit": {
      eventType = "DEPOSIT";
      const amount = (Math.random() * 490 + 10).toFixed(2);
      result = await client.deposit(account.id, amount, userToken);
      break;
    }
    case "withdraw": {
      eventType = "WITHDRAWAL";
      const amount = (Math.random() * 0.99 + 0.01).toFixed(2);
      result = await client.withdraw(account.id, amount, userToken);
      break;
    }
    case "grant-issue": {
      eventType = "GRANT_ISSUE";
      const amount = (Math.random() * 490 + 10).toFixed(2);
      const expiresAt = new Date(
        Date.now() + (Math.random() * 29 + 1) * 24 * 60 * 60 * 1000
      ).toISOString();
      const grantTypes = ["SIGNUP_BONUS", "PROMOTION", "MANUAL"];
      const grantType = grantTypes[Math.floor(Math.random() * grantTypes.length)];
      result = await client.issueGrant(account.id, amount, grantType, expiresAt, userToken);
      break;
    }
    case "grant-drawdown": {
      eventType = "GRANT_DRAWDOWN";
      const amount = (Math.random() * 0.99 + 0.01).toFixed(2);
      result = await client.drawdownGrant(account.id, amount, userToken);
      break;
    }
  }

  const msg: WorkerResult = {
    type: "result",
    success: result.success,
    latencyMs: result.latencyMs,
    errorType: result.errorType,
    eventType: eventType!,
    accountId: account.id,
  };
  process.send!(msg);
}

async function runBatch(size: number) {
  const ops = Array.from({ length: size }, makeBatchOp);
  const t0 = Date.now();
  process.send!({ type: "http_req" });
  let results;
  try {
    results = await client.batch(ops);
  } catch {
    // entire batch failed — report all as errors
    const latencyMs = Date.now() - t0;
    for (const op of ops) {
      process.send!({ type: "result", success: false, latencyMs, errorType: "other", eventType: op.op.toUpperCase(), accountId: op.account_id } as WorkerResult);
    }
    totalRequests += ops.length;
    return;
  }
  const latencyMs = Date.now() - t0;
  // Report each op individually so the reporter counts txns, not HTTP requests
  for (let i = 0; i < ops.length; i++) {
    const r = results[i];
    process.send!({
      type: "result",
      success: r.success,
      latencyMs,   // all ops in a batch share the same wall-clock latency
      errorType: r.success ? undefined : classifyBatchError(r.error),
      eventType: ops[i].op.toUpperCase(),
      accountId: ops[i].account_id,
    } as WorkerResult);
  }
  totalRequests += ops.length;
}

// Main loop: fire requests at target rate with concurrency limit
const deadline = Date.now() + config.duration * 1000;
let inflight = 0;
let totalRequests = 0;

const batchSize = config.batchSize > 0 ? config.batchSize : 0;
// concurrency = txn-level; divide by batchSize for HTTP-level concurrency
const httpConcurrency = batchSize > 0 ? Math.max(1, Math.ceil(config.concurrency / batchSize)) : config.concurrency;
const intervalMs = Math.max(1, Math.floor(1000 / config.rate));
const batchPerTick = Math.max(1, Math.ceil(config.rate / 1000));

const interval = setInterval(() => {
  if (Date.now() >= deadline) {
    clearInterval(interval);
    const drain = setInterval(() => {
      if (inflight <= 0) {
        clearInterval(drain);
        process.send!({ type: "done", workerId: config.workerId, totalRequests } as WorkerDone);
        // Exit cleanly so parent's "exit" handler fires
        setTimeout(() => process.exit(0), 100);
      }
    }, 50);
    return;
  }

  for (let i = 0; i < batchPerTick; i++) {
    if (inflight >= httpConcurrency) break;
    inflight++;
    if (batchSize > 0) {
      runBatch(batchSize).finally(() => { inflight--; });
    } else {
      totalRequests++;
      runTask().finally(() => { inflight--; });
    }
  }
}, intervalMs);
