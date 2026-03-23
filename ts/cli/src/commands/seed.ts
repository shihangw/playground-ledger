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

  const start = performance.now();
  let result;
  try {
    result = await client.seed(opts.count, opts.prefix, opts.initialBalance);
  } catch (err) {
    const msg = String(err);
    if (msg.includes("ECONNREFUSED") || msg.includes("fetch failed")) {
      console.error(`Error: Cannot connect to API at ${opts.apiUrl}`);
      console.error("Is the server running? Start it with: cd go && go run cmd/api/main.go");
      process.exit(1);
    }
    if ((err as Error).name === "TimeoutError") {
      console.error(`Error: Seed request timed out creating ${opts.count} users.`);
      console.error("Try fewer users: --count 20");
      process.exit(1);
    }
    console.error(`Error: ${msg}`);
    process.exit(1);
  }
  const elapsed = ((performance.now() - start) / 1000).toFixed(1);

  console.log(`Created ${result.created} users in ${elapsed}s`);

  if (result.errors && result.errors.length > 0) {
    console.log(`\nErrors (${result.errors.length}):`);
    for (const err of result.errors.slice(0, 10)) {
      console.log(`  - ${err}`);
    }
    if (result.errors.length > 10) {
      console.log(`  ... and ${result.errors.length - 10} more`);
    }
  }

  console.log(`\nSample users:`);
  for (const user of result.users.slice(0, 5)) {
    console.log(
      `  ${user.external_id} | account: ${user.account_id} | balance: $${user.balance}`
    );
  }
  if (result.users.length > 5) {
    console.log(`  ... and ${result.users.length - 5} more`);
  }
}
