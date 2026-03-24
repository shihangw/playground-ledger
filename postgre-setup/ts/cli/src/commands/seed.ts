import { LedgerClient } from "../lib/api-client.js";

export async function seedCommand(opts: {
  apiUrl: string;
  count: number;
  prefix: string;
  initialBalance: string;
}) {
  const client = new LedgerClient(opts.apiUrl);

  console.log(
    `Seeding ${opts.count} users with prefix "${opts.prefix}" and $${opts.initialBalance} balance...`
  );
  console.log(`API: ${opts.apiUrl}\n`);

  const BATCH = 10000;
  const start = performance.now();
  let totalCreated = 0;

  try {
    let remaining = opts.count;
    let startIndex = 0;
    while (remaining > 0) {
      const batch = Math.min(remaining, BATCH);
      const result = await client.seed(batch, opts.prefix, opts.initialBalance, startIndex);
      totalCreated += result.created;
      remaining -= batch;
      startIndex += batch;
      process.stdout.write(`\r  seeded ${totalCreated}/${opts.count}...`);
    }
    console.log();
  } catch (err) {
    const msg = String(err);
    if (msg.includes("ECONNREFUSED") || msg.includes("fetch failed")) {
      console.error(`\nError: Cannot connect to API at ${opts.apiUrl}`);
      console.error("Is the server running? Start it with: cd go && go run cmd/api/main.go");
      process.exit(1);
    }
    console.error(`\nError: ${msg}`);
    process.exit(1);
  }
  const elapsed = ((performance.now() - start) / 1000).toFixed(1);

  console.log(`Created ${totalCreated} users in ${elapsed}s`);
}
