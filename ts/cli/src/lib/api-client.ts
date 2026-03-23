import { SeedResponse, StressResult, AccountInfo } from "./types.js";

const REQUEST_TIMEOUT = 10_000; // 10s timeout for all requests

export class LedgerClient {
  constructor(private baseUrl: string) {}

  async seed(
    count: number,
    prefix: string,
    initialBalance: string
  ): Promise<SeedResponse> {
    const res = await fetch(`${this.baseUrl}/v1/admin/seed`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        count,
        prefix,
        initial_balance: initialBalance,
      }),
      signal: AbortSignal.timeout(30_000),
    });
    if (!res.ok) {
      const body = await res.text();
      throw new Error(`Seed failed (${res.status}): ${body}`);
    }
    return res.json();
  }

  async getUsers(
    token: string,
    limit = 100,
    offset = 0
  ): Promise<AccountInfo[]> {
    const res = await fetch(
      `${this.baseUrl}/v1/users?limit=${limit}&offset=${offset}`,
      {
        headers: { Authorization: `Bearer ${token}` },
        signal: AbortSignal.timeout(REQUEST_TIMEOUT),
      }
    );
    if (!res.ok) {
      throw new Error(`Get users failed (${res.status})`);
    }
    return res.json();
  }

  async deposit(
    accountId: string,
    amount: string,
    token: string
  ): Promise<StressResult> {
    const start = performance.now();
    try {
      const res = await fetch(
        `${this.baseUrl}/v1/accounts/${accountId}/deposit`,
        {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Authorization: `Bearer ${token}`,
            "Idempotency-Key": crypto.randomUUID(),
          },
          body: JSON.stringify({ amount, currency: "USD" }),
        }
      );
      const latencyMs = performance.now() - start;
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        return {
          success: false,
          latencyMs,
          error: (body as Record<string, string>).error || `HTTP ${res.status}`,
          errorType: classifyError(res.status, (body as Record<string, string>).error),
        };
      }
      return { success: true, latencyMs };
    } catch (err) {
      return {
        success: false,
        latencyMs: performance.now() - start,
        error: String(err),
        errorType: "timeout",
      };
    }
  }

  async withdraw(
    accountId: string,
    amount: string,
    token: string
  ): Promise<StressResult> {
    const start = performance.now();
    try {
      const res = await fetch(
        `${this.baseUrl}/v1/accounts/${accountId}/withdraw`,
        {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Authorization: `Bearer ${token}`,
            "Idempotency-Key": crypto.randomUUID(),
          },
          body: JSON.stringify({ amount }),
        }
      );
      const latencyMs = performance.now() - start;
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        return {
          success: false,
          latencyMs,
          error: (body as Record<string, string>).error || `HTTP ${res.status}`,
          errorType: classifyError(res.status, (body as Record<string, string>).error),
        };
      }
      return { success: true, latencyMs };
    } catch (err) {
      return {
        success: false,
        latencyMs: performance.now() - start,
        error: String(err),
        errorType: "timeout",
      };
    }
  }

  async issueGrant(
    accountId: string,
    amount: string,
    grantType: string,
    expiresAt: string,
    token: string
  ): Promise<StressResult> {
    const start = performance.now();
    try {
      const res = await fetch(
        `${this.baseUrl}/v1/accounts/${accountId}/grants`,
        {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Authorization: `Bearer ${token}`,
            "Idempotency-Key": crypto.randomUUID(),
          },
          body: JSON.stringify({
            amount,
            grant_type: grantType,
            expires_at: expiresAt,
          }),
        }
      );
      const latencyMs = performance.now() - start;
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        return {
          success: false,
          latencyMs,
          error: (body as Record<string, string>).error || `HTTP ${res.status}`,
          errorType: classifyError(res.status, (body as Record<string, string>).error),
        };
      }
      return { success: true, latencyMs };
    } catch (err) {
      return {
        success: false,
        latencyMs: performance.now() - start,
        error: String(err),
        errorType: "timeout",
      };
    }
  }

  async drawdownGrant(
    accountId: string,
    amount: string,
    token: string
  ): Promise<StressResult> {
    const start = performance.now();
    try {
      const res = await fetch(
        `${this.baseUrl}/v1/accounts/${accountId}/grants/drawdown`,
        {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Authorization: `Bearer ${token}`,
            "Idempotency-Key": crypto.randomUUID(),
          },
          body: JSON.stringify({ amount }),
        }
      );
      const latencyMs = performance.now() - start;
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        return {
          success: false,
          latencyMs,
          error: (body as Record<string, string>).error || `HTTP ${res.status}`,
          errorType: classifyError(res.status, (body as Record<string, string>).error),
        };
      }
      return { success: true, latencyMs };
    } catch (err) {
      return {
        success: false,
        latencyMs: performance.now() - start,
        error: String(err),
        errorType: "timeout",
      };
    }
  }

  async expireGrants(): Promise<{ expired_count: number }> {
    const res = await fetch(`${this.baseUrl}/v1/admin/grants/expire`, {
      method: "POST",
    });
    if (!res.ok) throw new Error(`Expire grants failed (${res.status})`);
    return res.json() as Promise<{ expired_count: number }>;
  }

  async logEvents(
    events: Array<{
      run_id: string;
      event_type: string;
      account_id: string;
      success: boolean;
      latency_ms: number;
      error_message?: string;
    }>
  ): Promise<void> {
    await fetch(`${this.baseUrl}/v1/admin/stress/events`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ events }),
    });
  }

  async getRunSummary(runId: string): Promise<Record<string, unknown>> {
    const res = await fetch(
      `${this.baseUrl}/v1/admin/stress/runs/${runId}`
    );
    if (!res.ok) throw new Error(`Failed to get run summary (${res.status})`);
    return res.json() as Promise<Record<string, unknown>>;
  }

  async listRuns(limit = 10): Promise<unknown[]> {
    const res = await fetch(
      `${this.baseUrl}/v1/admin/stress/runs?limit=${limit}`
    );
    if (!res.ok) throw new Error(`Failed to list runs (${res.status})`);
    return res.json() as Promise<unknown[]>;
  }
}

function classifyError(
  status: number,
  message?: string
): StressResult["errorType"] {
  if (message?.includes("insufficient grant")) return "insufficient_grants";
  if (message?.includes("insufficient funds")) return "insufficient_funds";
  if (status === 500) return "contention";
  if (status === 408 || status === 504) return "timeout";
  return "other";
}
