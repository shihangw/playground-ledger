import { program } from "commander";
import { seedCommand } from "./commands/seed.js";
import { stressCommand } from "./commands/stress.js";
import { metricsCommand } from "./commands/metrics.js";

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
    await seedCommand({
      apiUrl: opts.apiUrl,
      count: parseInt(opts.count),
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
    "Test mode: grants, consumption, or mixed",
    "mixed"
  )
  .option("--concurrency <n>", "Max concurrent requests", "20")
  .option("--duration <seconds>", "Test duration in seconds", "30")
  .option("--rate <rps>", "Target requests per second", "50")
  .option("--prefix <prefix>", "User ID prefix (must match seed)", "stress")
  .action(async (opts) => {
    const mode = opts.mode as "grants" | "consumption" | "mixed";
    if (!["grants", "consumption", "mixed"].includes(mode)) {
      console.error("Invalid mode. Use: grants, consumption, or mixed");
      process.exit(1);
    }
    await stressCommand({
      apiUrl: opts.apiUrl,
      mode,
      concurrency: parseInt(opts.concurrency),
      duration: parseInt(opts.duration),
      rate: parseInt(opts.rate),
      prefix: opts.prefix,
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

program.parse();
