import { StressStats } from "./types.js";

export class Reporter {
  private stats: StressStats = {
    totalRequests: 0,
    successCount: 0,
    errorCount: 0,
    errorsByType: {},
    latencies: [],
    startTime: Date.now(),
  };
  private httpRequests = 0;
  private lastHttpCount = 0;
  private interval: ReturnType<typeof setInterval> | null = null;
  private lastReportCount = 0;
  private lastReportTime = Date.now();

  start() {
    this.stats.startTime = Date.now();
    this.lastReportTime = Date.now();
    this.interval = setInterval(() => this.printLive(), 1000);
  }

  recordHttp() { this.httpRequests++; }

  record(success: boolean, latencyMs: number, errorType?: string) {
    this.stats.totalRequests++;
    this.stats.latencies.push(latencyMs);
    if (success) {
      this.stats.successCount++;
    } else {
      this.stats.errorCount++;
      if (errorType) {
        this.stats.errorsByType[errorType] =
          (this.stats.errorsByType[errorType] || 0) + 1;
      }
    }
  }

  private printLive() {
    const now = Date.now();
    const elapsed = (now - this.stats.startTime) / 1000;
    const recentCount = this.stats.totalRequests - this.lastReportCount;
    const recentDuration = (now - this.lastReportTime) / 1000;
    const txnPerSec = recentDuration > 0 ? recentCount / recentDuration : 0;
    const recentHttp = this.httpRequests - this.lastHttpCount;
    const reqPerSec = recentDuration > 0 ? recentHttp / recentDuration : 0;

    this.lastReportCount = this.stats.totalRequests;
    this.lastHttpCount = this.httpRequests;
    this.lastReportTime = now;

    const p50 = this.percentile(50);
    const p95 = this.percentile(95);
    const p99 = this.percentile(99);

    const contention = this.stats.errorsByType["contention"] || 0;
    const contentionRate = this.stats.totalRequests > 0
      ? (contention / this.stats.totalRequests) * 100
      : 0;

    const isBatch = this.httpRequests > 0 && this.stats.totalRequests > this.httpRequests * 1.5;
    const reqPart = isBatch
      ? `req/s: ${reqPerSec.toFixed(0)} | txn/s: ${txnPerSec.toFixed(0)}`
      : `txn/s: ${txnPerSec.toFixed(0)}`;

    console.log(
      `[${elapsed.toFixed(0)}s] ` +
        `txn: ${this.stats.totalRequests} | ` +
        `${reqPart} | ` +
        `ok: ${this.stats.successCount} | ` +
        `err: ${this.stats.errorCount} | ` +
        `ctn: ${contention} (${contentionRate.toFixed(1)}%) | ` +
        `p50: ${p50.toFixed(0)}ms | ` +
        `p95: ${p95.toFixed(0)}ms | ` +
        `p99: ${p99.toFixed(0)}ms`
    );
  }

  stop() {
    if (this.interval) {
      clearInterval(this.interval);
      this.interval = null;
    }
    this.stats.endTime = Date.now();
  }

  printSummary() {
    const elapsed =
      ((this.stats.endTime || Date.now()) - this.stats.startTime) / 1000;
    const avgRps = this.stats.totalRequests / elapsed;

    const avgReqPerSec = this.httpRequests / elapsed;
    console.log("\n\n--- Stress Test Summary ---");
    console.log(`Duration:         ${elapsed.toFixed(1)}s`);
    console.log(`Total Txns:       ${this.stats.totalRequests}`);
    console.log(`Avg txn/s:        ${avgRps.toFixed(1)}`);
    if (this.stats.totalRequests > this.httpRequests * 1.5) {
      console.log(`Total HTTP Reqs:  ${this.httpRequests}`);
      console.log(`Avg req/s:        ${avgReqPerSec.toFixed(1)}`);
      console.log(`Avg batch size:   ${(this.stats.totalRequests / Math.max(this.httpRequests, 1)).toFixed(1)}`);
    }
    console.log(`Success:        ${this.stats.successCount}`);
    console.log(`Errors:         ${this.stats.errorCount}`);
    console.log(`Success Rate:   ${((this.stats.successCount / Math.max(this.stats.totalRequests, 1)) * 100).toFixed(1)}%`);

    const contention = this.stats.errorsByType["contention"] || 0;
    const contentionRate = (contention / Math.max(this.stats.totalRequests, 1)) * 100;
    console.log(`Contention:     ${contention} (${contentionRate.toFixed(2)}% of requests)`);

    console.log(`\nLatency:`);
    console.log(`  p50: ${this.percentile(50).toFixed(1)}ms`);
    console.log(`  p95: ${this.percentile(95).toFixed(1)}ms`);
    console.log(`  p99: ${this.percentile(99).toFixed(1)}ms`);
    console.log(`  max: ${this.percentile(100).toFixed(1)}ms`);

    if (Object.keys(this.stats.errorsByType).length > 0) {
      console.log(`\nErrors by type:`);
      for (const [type, count] of Object.entries(this.stats.errorsByType)) {
        console.log(`  ${type}: ${count}`);
      }
    }
  }

  private percentile(p: number): number {
    if (this.stats.latencies.length === 0) return 0;
    const sorted = [...this.stats.latencies].sort((a, b) => a - b);
    const idx = Math.min(
      Math.ceil((p / 100) * sorted.length) - 1,
      sorted.length - 1
    );
    return sorted[idx];
  }
}
