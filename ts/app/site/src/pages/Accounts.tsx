import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Card } from '../components/Card';
import { Button } from '../components/Button';
import { useAPI } from '../hooks/useAPI';
import type { Account } from '../lib/api';

export function Accounts() {
  const api = useAPI();
  const queryClient = useQueryClient();
  const [selectedAccount, setSelectedAccount] = useState<Account | null>(null);
  const [amount, setAmount] = useState('');
  const [operation, setOperation] = useState<'deposit' | 'withdraw' | null>(
    null
  );

  const { data: accounts, isLoading } = useQuery({
    queryKey: ['accounts'],
    queryFn: () => api.getAccounts(),
  });

  const depositMutation = useMutation({
    mutationFn: ({ accountId, amount }: { accountId: string; amount: string }) =>
      api.deposit(accountId, amount),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['accounts'] });
      closeModal();
    },
  });

  const withdrawMutation = useMutation({
    mutationFn: ({ accountId, amount }: { accountId: string; amount: string }) =>
      api.withdraw(accountId, amount),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['accounts'] });
      closeModal();
    },
  });

  const closeModal = () => {
    setSelectedAccount(null);
    setOperation(null);
    setAmount('');
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedAccount || !operation || !amount) return;

    if (operation === 'deposit') {
      depositMutation.mutate({ accountId: selectedAccount.id, amount });
    } else {
      withdrawMutation.mutate({ accountId: selectedAccount.id, amount });
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold text-gray-900 dark:text-white">
          Accounts
        </h1>
      </div>

      <Card>
        {isLoading ? (
          <div className="text-center py-8 text-gray-500">Loading...</div>
        ) : accounts?.length === 0 ? (
          <div className="text-center py-8 text-gray-500">
            No accounts found. Make an API call to create one.
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="text-left text-sm text-gray-500 dark:text-gray-400">
                  <th className="pb-3 font-medium">Account ID</th>
                  <th className="pb-3 font-medium">User ID</th>
                  <th className="pb-3 font-medium">Currency</th>
                  <th className="pb-3 font-medium text-right">Balance</th>
                  <th className="pb-3 font-medium text-right">Pending</th>
                  <th className="pb-3 font-medium text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-200 dark:divide-gray-700">
                {accounts?.map((account) => (
                  <tr key={account.id}>
                    <td className="py-3 text-sm font-mono text-gray-900 dark:text-white">
                      {account.id.slice(0, 8)}...
                    </td>
                    <td className="py-3 text-sm font-mono text-gray-600 dark:text-gray-300">
                      {account.user_id.slice(0, 8)}...
                    </td>
                    <td className="py-3 text-sm text-gray-600 dark:text-gray-300">
                      {account.currency}
                    </td>
                    <td className="py-3 text-sm text-right font-medium text-gray-900 dark:text-white">
                      ${parseFloat(account.balance).toFixed(2)}
                    </td>
                    <td className="py-3 text-sm text-right text-gray-500 dark:text-gray-400">
                      ${parseFloat(account.pending_balance).toFixed(2)}
                    </td>
                    <td className="py-3 text-sm text-right space-x-2">
                      <Button
                        size="sm"
                        onClick={() => {
                          setSelectedAccount(account);
                          setOperation('deposit');
                        }}
                      >
                        Deposit
                      </Button>
                      <Button
                        size="sm"
                        variant="secondary"
                        onClick={() => {
                          setSelectedAccount(account);
                          setOperation('withdraw');
                        }}
                      >
                        Withdraw
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {/* Modal */}
      {selectedAccount && operation && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
          <Card className="w-full max-w-md">
            <h2 className="text-lg font-semibold text-gray-900 dark:text-white mb-4">
              {operation === 'deposit' ? 'Deposit Funds' : 'Withdraw Funds'}
            </h2>
            <form onSubmit={handleSubmit} className="space-y-4">
              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                  Account
                </label>
                <div className="text-sm font-mono text-gray-600 dark:text-gray-400">
                  {selectedAccount.id}
                </div>
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                  Current Balance
                </label>
                <div className="text-lg font-semibold text-gray-900 dark:text-white">
                  ${parseFloat(selectedAccount.balance).toFixed(2)}
                </div>
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                  Amount
                </label>
                <input
                  type="text"
                  value={amount}
                  onChange={(e) => setAmount(e.target.value)}
                  placeholder="0.00"
                  className="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white focus:ring-2 focus:ring-indigo-500 focus:border-transparent"
                />
              </div>
              <div className="flex space-x-3">
                <Button type="submit" className="flex-1">
                  {operation === 'deposit' ? 'Deposit' : 'Withdraw'}
                </Button>
                <Button
                  type="button"
                  variant="secondary"
                  onClick={closeModal}
                  className="flex-1"
                >
                  Cancel
                </Button>
              </div>
            </form>
          </Card>
        </div>
      )}
    </div>
  );
}
