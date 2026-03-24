export interface SeedUser {
  external_id: string;
  email: string;
  account_id: string;
  balance: string;
}

export interface SeedResponse {
  created: number;
  users: SeedUser[];
  errors?: string[];
}

export interface StressResult {
  success: boolean;
  latencyMs: number;
  error?: string;
  errorType?: "insufficient_funds" | "insufficient_grants" | "contention" | "timeout" | "other";
}

export interface StressStats {
  totalRequests: number;
  successCount: number;
  errorCount: number;
  errorsByType: Record<string, number>;
  latencies: number[];
  startTime: number;
  endTime?: number;
}

export interface AccountInfo {
  id: string;
  user_id: string;
  currency: string;
  balance: string;
}

export interface BatchOp {
  op: "deposit" | "withdraw";
  account_id: string;
  amount: string;
}

export interface BatchResult {
  success: boolean;
  error?: string;
}
