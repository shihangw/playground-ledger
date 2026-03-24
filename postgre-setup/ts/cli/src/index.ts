import { program } from "commander";
import os from "node:os";
import { seedCommand } from "./commands/seed.js";
import { stressCommand, StressMode } from "./commands/stress.js";
import { metricsCommand } from "./commands/metrics.js";
import { verifyCommand } from "./commands/verify.js";

const VALID_MODES: StressMode[] = [
  "deposits",
  "withdrawals",
  "mixed",
  "credit-grants",
  "credit-drawdown",
  "credit-mixed",
];

program
  .name("ledger-stress")
  .description("Stress testing CLI for the playground ledger");

program
  .command("seed")
  .description("Seed the database with test users")
  .option("--api-url <url>", "API base URL", "http://localhost:8080")
  .option("--count <n>", "Number of users to create", "100")
  .option("--prefix <prefix>", "User ID prefix", "stress")
  .option("--initial-balance <amount>", "Initial balance per user", "10000")
  .action(async (opts) => {
    const count = parseInt(opts.count);
    if (!count || count < 1) {
      console.error("--count must be a positive number");
      process.exit(1);
    }
    await seedCommand({
      apiUrl: opts.apiUrl,
      count,
      prefix: opts.prefix,
      initialBalance: opts.initialBalance,
    });
  });

program
  .command("stress")
  .description("Run stress tests against the ledger")
  .option("--api-url <url>", "API base URL", "http://localhost:8080")
  .option(
    "--mode <mode>",
    `Test mode: ${VALID_MODES.join(", ")}`,
    "mixed"
  )
  .option("--concurrency <n>", "Max concurrent requests", "20")
  .option("--duration <seconds>", "Test duration in seconds", "60")
  .option("--rate <rps>", "Target requests per second", "50")
  .option("--workers <n>", "Number of worker processes", String(os.cpus().length))
  .option("--batch-size <n>", "Transactions per batch request (0 = single requests)", "100")
  .option("--prefix <prefix>", "User ID prefix", "stress")
  .option("--count <n>", "Users to create if none exist (auto-seed)", "100")
  .option("--initial-balance <amount>", "Starting balance for auto-seeded users", "10000")
  .action(async (opts) => {
    const mode = opts.mode as StressMode;
    if (!VALID_MODES.includes(mode)) {
      console.error(`Invalid mode. Use: ${VALID_MODES.join(", ")}`);
      process.exit(1);
    }
    const rate = parseInt(opts.rate);
    const workers = parseInt(opts.workers);
    const batchSize = parseInt(opts.batchSize ?? "0");
    if (workers > os.cpus().length * 4) {
      console.warn(`Warning: ${workers} workers on ${os.cpus().length}-core machine may hurt performance due to context switching.`);
    }
    await stressCommand({
      apiUrl: opts.apiUrl,
      mode,
      concurrency: parseInt(opts.concurrency),
      duration: parseInt(opts.duration),
      rate,
      workers,
      batchSize,
      prefix: opts.prefix,
      count: parseInt(opts.count),
      initialBalance: opts.initialBalance,
    });
  });

program
  .command("metrics")
  .description("Query stress test metrics from the server")
  .option("--api-url <url>", "API base URL", "http://localhost:8080")
  .option("--run-id <id>", "Specific run ID to query (omit to list runs)")
  .option("--limit <n>", "Number of recent runs to show", "10")
  .action(async (opts) => {
    await metricsCommand({
      apiUrl: opts.apiUrl,
      runId: opts.runId,
      limit: parseInt(opts.limit),
    });
  });

program
  .command("verify")
  .description("Verify ledger correctness: deposit+withdraw invariant, transfer sum invariant, ledger reconciliation")
  .option("--api-url <url>", "API base URL", "http://localhost:8080")
  .option("--ops-per-account <n>", "Deposits and withdrawals per account for scenario 1", "5")
  .option("--transfer-count <n>", "Number of random transfers for scenario 2", "500")
  .option("--amount <n>", "Amount per operation", "1")
  .action(async (opts) => {
    await verifyCommand({
      apiUrl: opts.apiUrl,
      opsPerAccount: parseInt(opts.opsPerAccount),
      transferCount: parseInt(opts.transferCount),
      amount: opts.amount,
    });
  });

program.parse();
