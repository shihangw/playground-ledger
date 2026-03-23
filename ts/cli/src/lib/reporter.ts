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
  private interval: ReturnType<typeof setInterval> | null = null;
  private lastReportCount = 0;
  private lastReportTime = Date.now();

  start() {
    this.stats.startTime = Date.now();
    this.lastReportTime = Date.now();
    this.interval = setInterval(() => this.printLive(), 1000);
  }

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
    const currentRps = recentDuration > 0 ? recentCount / recentDuration : 0;

    this.lastReportCount = this.stats.totalRequests;
    this.lastReportTime = now;

    const p50 = this.percentile(50);
    const p95 = this.percentile(95);
    const p99 = this.percentile(99);

    console.log(
      `[${elapsed.toFixed(0)}s] ` +
        `req: ${this.stats.totalRequests} | ` +
        `rps: ${currentRps.toFixed(0)} | ` +
        `ok: ${this.stats.successCount} | ` +
        `err: ${this.stats.errorCount} | ` +
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

    console.log("\n\n--- Stress Test Summary ---");
    console.log(`Duration:       ${elapsed.toFixed(1)}s`);
    console.log(`Total Requests: ${this.stats.totalRequests}`);
    console.log(`Avg RPS:        ${avgRps.toFixed(1)}`);
    console.log(`Success:        ${this.stats.successCount}`);
    console.log(`Errors:         ${this.stats.errorCount}`);
    console.log(`Success Rate:   ${((this.stats.successCount / Math.max(this.stats.totalRequests, 1)) * 100).toFixed(1)}%`);
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
