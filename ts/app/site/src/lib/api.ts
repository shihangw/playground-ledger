// Use empty string for dev (Vite proxy handles /v1 -> localhost:8080)
const API_BASE = '';

export interface Account {
  id: string;
  user_id: string;
  currency: string;
  balance: string;
  pending_balance: string;
  created_at: string;
  updated_at: string;
}

export interface Transaction {
  id: string;
  transaction_type: string;
  status: string;
  source_account_id: string | null;
  destination_account_id: string | null;
  amount: string;
  currency: string;
  idempotency_key: string;
  created_at: string;
}

export interface TransactionRequest {
  amount: string;
  to_account_id?: string;
}

class LedgerAPI {
  private token: string;

  constructor(token: string) {
    this.token = token;
  }

  private async request<T>(
    path: string,
    options: RequestInit = {}
  ): Promise<T> {
    const response = await fetch(`${API_BASE}${path}`, {
      ...options,
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${this.token}`,
        ...options.headers,
      },
    });

    if (!response.ok) {
      const error = await response.text();
      throw new Error(error || `HTTP ${response.status}`);
    }

    return response.json();
  }

  async getAccounts(): Promise<Account[]> {
    return this.request<Account[]>('/v1/accounts');
  }

  async getAccount(accountId: string): Promise<Account> {
    return this.request<Account>(`/v1/accounts/${accountId}`);
  }

  async deposit(accountId: string, amount: string): Promise<Transaction> {
    return this.request<Transaction>(`/v1/accounts/${accountId}/deposit`, {
      method: 'POST',
      headers: {
        'Idempotency-Key': crypto.randomUUID(),
      },
      body: JSON.stringify({ amount }),
    });
  }

  async withdraw(accountId: string, amount: string): Promise<Transaction> {
    return this.request<Transaction>(`/v1/accounts/${accountId}/withdraw`, {
      method: 'POST',
      headers: {
        'Idempotency-Key': crypto.randomUUID(),
      },
      body: JSON.stringify({ amount }),
    });
  }

  async transfer(
    accountId: string,
    toAccountId: string,
    amount: string
  ): Promise<Transaction> {
    return this.request<Transaction>(`/v1/accounts/${accountId}/transfer`, {
      method: 'POST',
      headers: {
        'Idempotency-Key': crypto.randomUUID(),
      },
      body: JSON.stringify({ to_account_id: toAccountId, amount }),
    });
  }

  async getTransactions(accountId: string): Promise<Transaction[]> {
    return this.request<Transaction[]>(`/v1/accounts/${accountId}/transactions`);
  }

  async getAllUsers(): Promise<Account[]> {
    return this.request<Account[]>('/v1/users');
  }
}

export function createAPI(token: string): LedgerAPI {
  return new LedgerAPI(token);
}
