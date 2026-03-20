import { LedgerClient } from "../lib/api-client.js";
import { Reporter } from "../lib/reporter.js";
import { AccountInfo } from "../lib/types.js";

interface StressOpts {
  apiUrl: string;
  mode: "grants" | "consumption" | "mixed";
  concurrency: number;
  duration: number; // seconds
  rate: number; // target requests per second
  prefix: string;
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

  console.log(`Stress test: mode=${opts.mode} concurrency=${opts.concurrency} rate=${opts.rate}/s duration=${opts.duration}s`);
  console.log(`Run ID: ${runId}`);
  console.log(`API: ${opts.apiUrl}`);

  // Fetch users
  console.log("Fetching user accounts...");
  const token = `dev_${opts.prefix}_user_0`;
  const accounts = await client.getUsers(token, 100, 0);

  if (accounts.length === 0) {
    console.error("No users found. Run 'seed' first.");
    process.exit(1);
  }
  console.log(`Found ${accounts.length} accounts\n`);

  const deadline = Date.now() + opts.duration * 1000;
  const delayMs = 1000 / opts.rate;
  let inflight = 0;

  // Buffer events and flush in batches
  let eventBuffer: PendingEvent[] = [];
  const FLUSH_SIZE = 50;

  const flushEvents = async () => {
    if (eventBuffer.length === 0) return;
    const batch = eventBuffer;
    eventBuffer = [];
    try {
      await client.logEvents(batch);
    } catch {
      // Don't let event logging failures affect the stress test
    }
  };

  reporter.start();

  const runTask = async (
    type: "grant" | "consume",
    account: AccountInfo
  ) => {
    const userToken = `dev_${account.user_id}`;
    const eventType = type === "grant" ? "DEPOSIT" : "WITHDRAWAL";
    let result;
    if (type === "grant") {
      // Random grant: $10 - $500
      const amount = (Math.random() * 490 + 10).toFixed(2);
      result = await client.deposit(account.id, amount, userToken);
    } else {
      // Small consumption: $0.01 - $1.00
      const amount = (Math.random() * 0.99 + 0.01).toFixed(2);
      result = await client.withdraw(account.id, amount, userToken);
    }
    reporter.record(result.success, result.latencyMs, result.errorType);

    // Buffer event for server-side logging
    eventBuffer.push({
      run_id: runId,
      event_type: eventType,
      account_id: account.id,
      success: result.success,
      latency_ms: result.latencyMs,
      error_message: result.error,
    });
    if (eventBuffer.length >= FLUSH_SIZE) {
      flushEvents();
    }

    inflight--;
  };

  const pickRandom = () => accounts[Math.floor(Math.random() * accounts.length)];

  // Main loop - emit requests at target rate
  await new Promise<void>((resolve) => {
    const tick = () => {
      if (Date.now() >= deadline) {
        resolve();
        return;
      }

      if (inflight < opts.concurrency) {
        inflight++;
        const account = pickRandom();

        let taskType: "grant" | "consume";
        if (opts.mode === "grants") {
          taskType = "grant";
        } else if (opts.mode === "consumption") {
          taskType = "consume";
        } else {
          // mixed: 30% grants, 70% consumption
          taskType = Math.random() < 0.3 ? "grant" : "consume";
        }

        runTask(taskType, account);
      }

      setTimeout(tick, delayMs);
    };
    tick();
  });

  // Wait for inflight to drain (max 10s)
  const drainDeadline = Date.now() + 10000;
  while (inflight > 0 && Date.now() < drainDeadline) {
    await new Promise((r) => setTimeout(r, 100));
  }

  // Flush remaining events
  await flushEvents();

  reporter.stop();
  reporter.printSummary();

  console.log(`\nRun ID: ${runId}`);
  console.log(`Query server-side metrics: ledger-stress metrics --run-id ${runId}`);
}
