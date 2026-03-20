import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Card } from '../components/Card';
import { useAPI } from '../hooks/useAPI';
import type { Account } from '../lib/api';

export function Transactions() {
  const api = useAPI();
  const [selectedAccountId, setSelectedAccountId] = useState<string>('');

  const { data: accounts } = useQuery({
    queryKey: ['accounts'],
    queryFn: () => api.getAccounts(),
  });

  const { data: transactions, isLoading } = useQuery({
    queryKey: ['transactions', selectedAccountId],
    queryFn: () => api.getTransactions(selectedAccountId),
    enabled: !!selectedAccountId,
  });

  const getTransactionBadge = (type: string) => {
    const styles: Record<string, string> = {
      DEPOSIT: 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-300',
      WITHDRAWAL: 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-300',
      TRANSFER: 'bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-300',
    };
    return styles[type] || 'bg-gray-100 text-gray-800';
  };

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold text-gray-900 dark:text-white">
        Transactions
      </h1>

      <Card>
        <div className="mb-6">
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
            Select Account
          </label>
          <select
            value={selectedAccountId}
            onChange={(e) => setSelectedAccountId(e.target.value)}
            className="w-full md:w-auto px-4 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white focus:ring-2 focus:ring-indigo-500"
          >
            <option value="">Select an account...</option>
            {accounts?.map((account: Account) => (
              <option key={account.id} value={account.id}>
                {account.id.slice(0, 8)}... ({account.currency})
              </option>
            ))}
          </select>
        </div>

        {!selectedAccountId ? (
          <div className="text-center py-8 text-gray-500">
            Select an account to view transactions
          </div>
        ) : isLoading ? (
          <div className="text-center py-8 text-gray-500">Loading...</div>
        ) : transactions?.length === 0 ? (
          <div className="text-center py-8 text-gray-500">
            No transactions found for this account
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="text-left text-sm text-gray-500 dark:text-gray-400">
                  <th className="pb-3 font-medium">Transaction ID</th>
                  <th className="pb-3 font-medium">Type</th>
                  <th className="pb-3 font-medium">Status</th>
                  <th className="pb-3 font-medium text-right">Amount</th>
                  <th className="pb-3 font-medium text-right">Date</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-200 dark:divide-gray-700">
                {transactions?.map((tx) => (
                  <tr key={tx.id}>
                    <td className="py-3 text-sm font-mono text-gray-900 dark:text-white">
                      {tx.id.slice(0, 8)}...
                    </td>
                    <td className="py-3">
                      <span
                        className={`inline-flex px-2 py-1 text-xs font-medium rounded-full ${getTransactionBadge(
                          tx.transaction_type
                        )}`}
                      >
                        {tx.transaction_type}
                      </span>
                    </td>
                    <td className="py-3 text-sm text-gray-600 dark:text-gray-300">
                      {tx.status}
                    </td>
                    <td className="py-3 text-sm text-right font-medium text-gray-900 dark:text-white">
                      ${parseFloat(tx.amount).toFixed(2)}
                    </td>
                    <td className="py-3 text-sm text-right text-gray-500 dark:text-gray-400">
                      {new Date(tx.created_at).toLocaleString()}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </div>
  );
}
