import { SeedResponse, StressResult, AccountInfo, BatchOp, BatchResult } from "./types.js";

const REQUEST_TIMEOUT = 10_000; // 10s timeout for all requests

export class LedgerClient {
  constructor(private baseUrl: string) {}

  async seed(
    count: number,
    prefix: string,
    initialBalance: string,
    startIndex = 0
  ): Promise<SeedResponse> {
    const res = await fetch(`${this.baseUrl}/v1/admin/seed`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        count,
        prefix,
        initial_balance: initialBalance,
        start_index: startIndex,
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

  async batch(ops: BatchOp[]): Promise<BatchResult[]> {
    const res = await fetch(`${this.baseUrl}/v1/batch`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(ops),
      signal: AbortSignal.timeout(REQUEST_TIMEOUT),
    });
    if (!res.ok) throw new Error(`Batch failed (${res.status})`);
    return res.json() as Promise<BatchResult[]>;
  }

  async transfer(
    fromAccountId: string,
    toAccountId: string,
    amount: string,
    token: string
  ): Promise<StressResult> {
    const start = performance.now();
    try {
      const res = await fetch(
        `${this.baseUrl}/v1/accounts/${fromAccountId}/transfer`,
        {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Authorization: `Bearer ${token}`,
            "Idempotency-Key": crypto.randomUUID(),
          },
          body: JSON.stringify({ to_account_id: toAccountId, amount }),
          signal: AbortSignal.timeout(REQUEST_TIMEOUT),
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

  async getAllUsers(limit = 1000): Promise<Array<{ id: string; user_id: string; currency: string; balance: string }>> {
    const res = await fetch(`${this.baseUrl}/v1/users?limit=${limit}`, {
      headers: { Authorization: "Bearer dev_stress_user_1" },
      signal: AbortSignal.timeout(REQUEST_TIMEOUT),
    });
    if (!res.ok) throw new Error(`getAllUsers failed (${res.status})`);
    return res.json();
  }

  async getAccount(accountId: string, token: string): Promise<{ account_id: string; balance: string }> {
    const res = await fetch(`${this.baseUrl}/v1/accounts/${accountId}`, {
      headers: { Authorization: `Bearer ${token}` },
      signal: AbortSignal.timeout(REQUEST_TIMEOUT),
    });
    if (!res.ok) throw new Error(`getAccount failed (${res.status})`);
    return res.json();
  }

  async reconcile(sample?: number): Promise<{ ok: boolean; discrepancy_count: number; discrepancies: Array<{ account_id: string; account_balance: string; ledger_balance: string; discrepancy: string; entry_count: number }> }> {
    const url = sample
      ? `${this.baseUrl}/v1/admin/reconcile?sample=${sample}`
      : `${this.baseUrl}/v1/admin/reconcile`;
    const res = await fetch(url, { signal: AbortSignal.timeout(60_000) });
    if (!res.ok) throw new Error(`reconcile failed (${res.status}): ${await res.text()}`);
    return res.json();
  }
}

function classifyError(
  status: number,
  message?: string
): StressResult["errorType"] {
  if (message?.includes("insufficient grant")) return "insufficient_grants";
  if (message?.includes("insufficient funds")) return "insufficient_funds";
  if (status === 409) return "contention";
  if (status === 408 || status === 504) return "timeout";
  return "other";
}
