import { LedgerClient } from "../lib/api-client.js";

export async function metricsCommand(opts: {
  apiUrl: string;
  runId?: string;
  limit: number;
}) {
  const client = new LedgerClient(opts.apiUrl);

  if (!opts.runId) {
    // List recent runs
    console.log("Recent stress test runs:\n");
    const runs = await client.listRuns(opts.limit);
    if ((runs as unknown[]).length === 0) {
      console.log("No runs found.");
      return;
    }
    console.log(
      "Run ID".padEnd(36) +
        "Events".padEnd(10) +
        "Started".padEnd(28) +
        "Ended"
    );
    console.log("-".repeat(100));
    for (const run of runs as Array<Record<string, unknown>>) {
      console.log(
        String(run.run_id).padEnd(36) +
          String(run.total_events).padEnd(10) +
          String(run.started_at).padEnd(28) +
          String(run.ended_at)
      );
    }
    console.log(`\nUse: ledger-stress metrics --run-id <id> for details`);
    return;
  }

  // Get run summary
  const data = await client.getRunSummary(opts.runId);
  const summary = data.summary as Array<Record<string, unknown>>;
  const qps = data.qps as Array<Record<string, unknown>>;

  console.log(`\n--- Stress Run: ${opts.runId} ---\n`);

  if (!summary || summary.length === 0) {
    console.log("No events found for this run.");
    return;
  }

  // Summary table
  console.log("By Event Type:");
  console.log(
    "Type".padEnd(14) +
      "Total".padEnd(10) +
      "OK".padEnd(10) +
      "Err".padEnd(10) +
      "Avg(ms)".padEnd(12) +
      "p50(ms)".padEnd(12) +
      "p95(ms)".padEnd(12) +
      "p99(ms)".padEnd(12) +
      "Max(ms)"
  );
  console.log("-".repeat(100));

  for (const row of summary) {
    console.log(
      String(row.event_type).padEnd(14) +
        String(row.total_count).padEnd(10) +
        String(row.success_count).padEnd(10) +
        String(row.error_count).padEnd(10) +
        Number(row.avg_latency_ms).toFixed(1).padEnd(12) +
        Number(row.p50_latency_ms).toFixed(1).padEnd(12) +
        Number(row.p95_latency_ms).toFixed(1).padEnd(12) +
        Number(row.p99_latency_ms).toFixed(1).padEnd(12) +
        Number(row.max_latency_ms).toFixed(1)
    );
  }

  // Totals
  const totalReqs = summary.reduce(
    (s, r) => s + Number(r.total_count),
    0
  );
  const totalOk = summary.reduce(
    (s, r) => s + Number(r.success_count),
    0
  );
  const totalErr = summary.reduce(
    (s, r) => s + Number(r.error_count),
    0
  );
  console.log("-".repeat(100));
  console.log(
    "TOTAL".padEnd(14) +
      String(totalReqs).padEnd(10) +
      String(totalOk).padEnd(10) +
      String(totalErr).padEnd(10)
  );
  console.log(
    `\nSuccess Rate: ${((totalOk / Math.max(totalReqs, 1)) * 100).toFixed(1)}%`
  );

  // QPS over time
  if (qps && qps.length > 0) {
    console.log(`\nQPS Over Time (${qps.length} seconds):`);
    const maxQps = Math.max(...qps.map((q) => Number(q.request_count)));
    const barWidth = 40;
    for (const q of qps) {
      const count = Number(q.request_count);
      const ok = Number(q.success_count);
      const barLen = Math.round((count / maxQps) * barWidth);
      const bar = "#".repeat(barLen);
      const second = String(q.second).slice(11, 19); // HH:MM:SS
      console.log(
        `  ${second} | ${bar.padEnd(barWidth)} ${count} req (${ok} ok)`
      );
    }
    const avgQps = totalReqs / qps.length;
    console.log(`\nAvg QPS: ${avgQps.toFixed(1)}`);
  }
}
