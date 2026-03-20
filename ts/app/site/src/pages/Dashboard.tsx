import { useQuery } from '@tanstack/react-query';
import { Card } from '../components/Card';
import { useAPI } from '../hooks/useAPI';

export function Dashboard() {
  const api = useAPI();

  const { data: accounts, isLoading } = useQuery({
    queryKey: ['accounts'],
    queryFn: () => api.getAccounts(),
  });

  const totalBalance = accounts?.reduce(
    (sum, acc) => sum + parseFloat(acc.balance),
    0
  );

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold text-gray-900 dark:text-white">
        Dashboard
      </h1>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
        <Card>
          <div className="text-sm font-medium text-gray-500 dark:text-gray-400">
            Total Accounts
          </div>
          <div className="mt-1 text-3xl font-semibold text-gray-900 dark:text-white">
            {isLoading ? '...' : accounts?.length ?? 0}
          </div>
        </Card>

        <Card>
          <div className="text-sm font-medium text-gray-500 dark:text-gray-400">
            Total Balance
          </div>
          <div className="mt-1 text-3xl font-semibold text-gray-900 dark:text-white">
            {isLoading ? '...' : `$${totalBalance?.toFixed(2) ?? '0.00'}`}
          </div>
        </Card>

        <Card>
          <div className="text-sm font-medium text-gray-500 dark:text-gray-400">
            System Status
          </div>
          <div className="mt-1 flex items-center">
            <span className="w-3 h-3 rounded-full bg-green-500 mr-2"></span>
            <span className="text-lg font-medium text-gray-900 dark:text-white">
              Healthy
            </span>
          </div>
        </Card>
      </div>

      {accounts && accounts.length > 0 && (
        <Card title="Recent Accounts">
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="text-left text-sm text-gray-500 dark:text-gray-400">
                  <th className="pb-3 font-medium">Account ID</th>
                  <th className="pb-3 font-medium">Currency</th>
                  <th className="pb-3 font-medium text-right">Balance</th>
                  <th className="pb-3 font-medium text-right">Created</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-200 dark:divide-gray-700">
                {accounts.slice(0, 5).map((account) => (
                  <tr key={account.id}>
                    <td className="py-3 text-sm font-mono text-gray-900 dark:text-white">
                      {account.id.slice(0, 8)}...
                    </td>
                    <td className="py-3 text-sm text-gray-600 dark:text-gray-300">
                      {account.currency}
                    </td>
                    <td className="py-3 text-sm text-right font-medium text-gray-900 dark:text-white">
                      ${parseFloat(account.balance).toFixed(2)}
                    </td>
                    <td className="py-3 text-sm text-right text-gray-500 dark:text-gray-400">
                      {new Date(account.created_at).toLocaleDateString()}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      )}
    </div>
  );
}
