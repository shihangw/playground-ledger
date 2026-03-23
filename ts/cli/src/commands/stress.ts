import { fork } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";
import os from "node:os";
import { LedgerClient } from "../lib/api-client.js";
import { Reporter } from "../lib/reporter.js";
import { AccountInfo } from "../lib/types.js";

export type StressMode =
  | "deposits"
  | "withdrawals"
  | "mixed"
  | "credit-grants"
  | "credit-drawdown"
  | "credit-mixed";

interface StressOpts {
  apiUrl: string;
  mode: StressMode;
  concurrency: number;
  duration: number;
  rate: number;
  prefix: string;
  workers: number;
}

interface PendingEvent {
  run_id: string;
  event_type: string;
  account_id: string;
  success: boolean;
  latency_ms: number;
  error_message?: string;
}

export async function stressCommand(opts: StressOpts) {
  const client = new LedgerClient(opts.apiUrl);
  const reporter = new Reporter();
  const runId = `run_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`;

  const numWorkers = Math.min(opts.workers, opts.rate); // no point in more workers than RPS

  console.log(
    `Stress test: mode=${opts.mode} rate=${opts.rate}/s concurrency=${opts.concurrency} ` +
    `duration=${opts.duration}s workers=${numWorkers}`
  );
  console.log(`Run ID: ${runId}`);
  console.log(`API: ${opts.apiUrl}`);

  // Fetch users
  console.log("Fetching user accounts...");
  const token = `dev_${opts.prefix}_user_0`;
  let accounts: AccountInfo[];
  try {
    accounts = await client.getUsers(token, 100, 0);
  } catch (err) {
    const msg = String(err);
    if (msg.includes("ECONNREFUSED") || msg.includes("fetch failed")) {
      console.error(`\nError: Cannot connect to API at ${opts.apiUrl}`);
      console.error("Is the server running? Start it with: cd go && go run cmd/api/main.go");
      process.exit(1);
    }
    console.error(`\nError fetching users: ${msg}`);
    process.exit(1);
  }

  if (accounts.length === 0) {
    console.error("No users found. Run 'seed' first.");
    process.exit(1);
  }
  console.log(`Found ${accounts.length} accounts`);

  // Seed grants for drawdown modes
  if (opts.mode === "credit-drawdown" || opts.mode === "credit-mixed") {
    console.log("Seeding initial grants for drawdown testing...");
    const expiresAt = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString();
    let seeded = 0;
    for (const account of accounts) {
      const userToken = `dev_${account.user_id}`;
      const result = await client.issueGrant(
        account.id, "1000000.00", "PROMOTION", expiresAt, userToken
      );
      if (result.success) seeded++;
    }
    console.log(`Seeded grants for ${seeded}/${accounts.length} accounts`);
  }

  // Event buffer for server-side logging
  let eventBuffer: PendingEvent[] = [];
  const FLUSH_SIZE = 100;
  const flushEvents = async () => {
    if (eventBuffer.length === 0) return;
    const batch = eventBuffer;
    eventBuffer = [];
    try { await client.logEvents(batch); } catch { /* ignore */ }
  };

  // Resolve worker script path
  const thisFile = fileURLToPath(import.meta.url);
  const workerScript = path.resolve(path.dirname(thisFile), "../lib/worker.ts");

  // Split rate and concurrency across workers
  const perWorkerRate = Math.ceil(opts.rate / numWorkers);
  const perWorkerConcurrency = Math.ceil(opts.concurrency / numWorkers);

  console.log(`\nStarting ${numWorkers} workers (${perWorkerRate} rps × ${perWorkerConcurrency} concurrency each)...\n`);

  reporter.start();

  let workersCompleted = 0;

  await new Promise<void>((resolve) => {
    for (let w = 0; w < numWorkers; w++) {
      const workerConfig = {
        apiUrl: opts.apiUrl,
        mode: opts.mode,
        accounts,
        rate: perWorkerRate,
        concurrency: perWorkerConcurrency,
        duration: opts.duration,
        runId,
        workerId: w,
      };

      const child = fork(workerScript, [JSON.stringify(workerConfig)], {
        execArgv: ["--import", "tsx"],
        stdio: ["ignore", "inherit", "inherit", "ipc"],
      });

      child.on("message", (msg: { type: string; [key: string]: unknown }) => {
        if (msg.type === "result") {
          const r = msg as {
            type: string;
            success: boolean;
            latencyMs: number;
            errorType?: string;
            eventType: string;
            accountId: string;
          };
          reporter.record(r.success, r.latencyMs, r.errorType);

          eventBuffer.push({
            run_id: runId,
            event_type: r.eventType,
            account_id: r.accountId,
            success: r.success,
            latency_ms: r.latencyMs,
            error_message: r.errorType,
          });
          if (eventBuffer.length >= FLUSH_SIZE) {
            flushEvents();
          }
        } else if (msg.type === "done") {
          workersCompleted++;
          if (workersCompleted >= numWorkers) {
            resolve();
          }
        }
      });

      child.on("error", (err) => {
        console.error(`Worker ${w} error: ${err.message}`);
      });

      child.on("exit", (code) => {
        if (code !== 0 && code !== null) {
          console.error(`Worker ${w} exited with code ${code}`);
        }
        workersCompleted++;
        if (workersCompleted >= numWorkers) {
          resolve();
        }
      });
    }
  });

  // Flush remaining events
  await flushEvents();

  reporter.stop();
  reporter.printSummary();

  console.log(`\nRun ID: ${runId}`);
  console.log(`Query server-side metrics: ledger-stress metrics --run-id ${runId}`);
}
