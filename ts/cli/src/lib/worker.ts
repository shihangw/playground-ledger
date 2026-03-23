// Worker process for stress testing.
// Spawned by the stress command to run requests in parallel across processes.
// Communicates results back to parent via IPC messages.

import { LedgerClient } from "./api-client.js";
import { AccountInfo } from "./types.js";

interface WorkerConfig {
  apiUrl: string;
  mode: string;
  accounts: AccountInfo[];
  rate: number; // per-worker RPS
  concurrency: number; // per-worker concurrency
  duration: number; // seconds
  runId: string;
  workerId: number;
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
const accounts = config.accounts;

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

async function runTask() {
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

// Main loop: fire requests at target rate with concurrency limit
const deadline = Date.now() + config.duration * 1000;
let inflight = 0;
let totalRequests = 0;

// Use setInterval for more consistent timing than recursive setTimeout
const intervalMs = Math.max(1, Math.floor(1000 / config.rate));
let batchPerTick = Math.max(1, Math.ceil(config.rate / 1000));

const interval = setInterval(() => {
  if (Date.now() >= deadline) {
    clearInterval(interval);
    // Wait for inflight to drain
    const drain = setInterval(() => {
      if (inflight <= 0) {
        clearInterval(drain);
        const done: WorkerDone = {
          type: "done",
          workerId: config.workerId,
          totalRequests,
        };
        process.send!(done);
      }
    }, 50);
    return;
  }

  // Fire multiple requests per tick to achieve rates > 1000/s
  for (let i = 0; i < batchPerTick; i++) {
    if (inflight >= config.concurrency) break;
    inflight++;
    totalRequests++;
    runTask().finally(() => { inflight--; });
  }
}, intervalMs);
