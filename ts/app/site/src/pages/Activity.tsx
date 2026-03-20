import { useQuery } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { useAPI } from '../hooks/useAPI';

export function Activity() {
  const api = useAPI();
  const navigate = useNavigate();

  const { data: accounts } = useQuery({
    queryKey: ['accounts'],
    queryFn: () => api.getAccounts(),
  });

  const primaryAccount = accounts?.[0];

  const { data: transactions, isLoading } = useQuery({
    queryKey: ['transactions', primaryAccount?.id],
    queryFn: () => api.getTransactions(primaryAccount!.id),
    enabled: !!primaryAccount,
  });

  const getTransactionDisplay = (tx: {
    transaction_type: string;
    source_account_id: string | null;
    destination_account_id: string | null;
    amount: string;
  }) => {
    const isCredit = tx.destination_account_id === primaryAccount?.id;
    const amount = parseFloat(tx.amount);

    if (tx.transaction_type === 'DEPOSIT') {
      return {
        icon: '↓',
        label: 'Deposit',
        amount: `+$${amount.toFixed(2)}`,
        color: 'text-green-500',
      };
    }
    if (tx.transaction_type === 'WITHDRAWAL') {
      return {
        icon: '↑',
        label: 'Withdrawal',
        amount: `-$${amount.toFixed(2)}`,
        color: 'text-red-500',
      };
    }
    if (isCredit) {
      return {
        icon: '↓',
        label: 'Received',
        amount: `+$${amount.toFixed(2)}`,
        color: 'text-green-500',
      };
    }
    return {
      icon: '↑',
      label: 'Sent',
      amount: `-$${amount.toFixed(2)}`,
      color: 'text-white',
    };
  };

  return (
    <div className="min-h-screen bg-black text-white flex flex-col">
      <header className="flex items-center justify-between p-4 border-b border-gray-800">
        <button
          onClick={() => navigate('/')}
          className="text-green-500 font-medium"
        >
          Done
        </button>
        <h1 className="text-lg font-semibold">Activity</h1>
        <div className="w-12" />
      </header>

      <div className="flex-1 overflow-y-auto">
        {isLoading ? (
          <div className="flex items-center justify-center py-12">
            <p className="text-gray-500">Loading...</p>
          </div>
        ) : transactions?.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-12">
            <p className="text-gray-500 mb-2">No transactions yet</p>
            <p className="text-gray-600 text-sm">
              Send money to get started
            </p>
          </div>
        ) : (
          <div className="divide-y divide-gray-800">
            {transactions?.map((tx) => {
              const display = getTransactionDisplay(tx);
              return (
                <div
                  key={tx.id}
                  className="flex items-center justify-between p-4"
                >
                  <div className="flex items-center gap-4">
                    <div className="w-10 h-10 bg-gray-800 rounded-full flex items-center justify-center text-lg">
                      {display.icon}
                    </div>
                    <div>
                      <p className="font-medium">{display.label}</p>
                      <p className="text-sm text-gray-500">
                        {new Date(tx.created_at).toLocaleDateString('en-US', {
                          month: 'short',
                          day: 'numeric',
                          hour: 'numeric',
                          minute: '2-digit',
                        })}
                      </p>
                    </div>
                  </div>
                  <span className={`font-semibold ${display.color}`}>
                    {display.amount}
                  </span>
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
