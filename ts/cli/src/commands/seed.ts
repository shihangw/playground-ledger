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
  console.log(`API: ${opts.apiUrl}`);

  const start = performance.now();
  const result = await client.seed(
    opts.count,
    opts.prefix,
    opts.initialBalance
  );
  const elapsed = ((performance.now() - start) / 1000).toFixed(1);

  console.log(`\nCreated ${result.created} users in ${elapsed}s`);

  if (result.errors && result.errors.length > 0) {
    console.log(`\nErrors (${result.errors.length}):`);
    for (const err of result.errors.slice(0, 10)) {
      console.log(`  - ${err}`);
    }
    if (result.errors.length > 10) {
      console.log(`  ... and ${result.errors.length - 10} more`);
    }
  }

  // Print first few users as sample
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
