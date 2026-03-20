import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { useAPI } from '../hooks/useAPI';
import type { Account } from '../lib/api';

export function Send() {
  const api = useAPI();
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const [step, setStep] = useState<'select' | 'amount' | 'confirm'>('select');
  const [selectedUser, setSelectedUser] = useState<Account | null>(null);
  const [amount, setAmount] = useState('');
  const [searchQuery, setSearchQuery] = useState('');

  const { data: myAccounts } = useQuery({
    queryKey: ['accounts'],
    queryFn: () => api.getAccounts(),
  });

  const { data: allUsers } = useQuery({
    queryKey: ['all-users'],
    queryFn: () => api.getAllUsers(),
  });

  const myAccount = myAccounts?.[0];
  const myBalance = myAccount ? parseFloat(myAccount.balance) : 0;

  const transferMutation = useMutation({
    mutationFn: () => {
      if (!myAccount || !selectedUser) throw new Error('Missing accounts');
      return api.transfer(myAccount.id, selectedUser.id, amount);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['accounts'] });
      navigate('/success', { state: { amount, recipient: selectedUser } });
    },
  });

  const filteredUsers = allUsers?.filter(
    (user) =>
      user.id !== myAccount?.id &&
      (user.user_id.toLowerCase().includes(searchQuery.toLowerCase()) ||
        user.id.toLowerCase().includes(searchQuery.toLowerCase()))
  );

  const handleAmountKey = (key: string) => {
    if (key === 'back') {
      setAmount((prev) => prev.slice(0, -1));
    } else if (key === '.') {
      if (!amount.includes('.')) {
        setAmount((prev) => (prev || '0') + '.');
      }
    } else {
      const newAmount = amount + key;
      if (parseFloat(newAmount) <= myBalance) {
        setAmount(newAmount);
      }
    }
  };

  if (step === 'select') {
    return (
      <div className="min-h-screen bg-black text-white flex flex-col">
        <header className="flex items-center justify-between p-4">
          <button
            onClick={() => navigate('/')}
            className="text-green-500 font-medium"
          >
            Cancel
          </button>
          <h1 className="text-lg font-semibold">Send To</h1>
          <div className="w-16" />
        </header>

        <div className="px-4 pb-4">
          <input
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="Search by username..."
            className="w-full bg-gray-900 text-white px-4 py-3 rounded-xl focus:outline-none focus:ring-2 focus:ring-green-500"
          />
        </div>

        <div className="flex-1 overflow-y-auto px-4">
          {filteredUsers?.length === 0 ? (
            <p className="text-gray-500 text-center py-8">No users found</p>
          ) : (
            <div className="space-y-2">
              {filteredUsers?.map((user) => (
                <button
                  key={user.id}
                  onClick={() => {
                    setSelectedUser(user);
                    setStep('amount');
                  }}
                  className="w-full flex items-center gap-4 p-4 bg-gray-900 rounded-xl hover:bg-gray-800 transition-colors"
                >
                  <div className="w-12 h-12 bg-gradient-to-br from-green-400 to-green-600 rounded-full flex items-center justify-center text-black font-bold text-lg">
                    {user.user_id.slice(0, 1).toUpperCase()}
                  </div>
                  <div className="flex-1 text-left">
                    <p className="font-medium">{user.user_id}</p>
                    <p className="text-sm text-gray-500 font-mono">
                      {user.id.slice(0, 8)}...
                    </p>
                  </div>
                </button>
              ))}
            </div>
          )}
        </div>
      </div>
    );
  }

  if (step === 'amount') {
    return (
      <div className="min-h-screen bg-black text-white flex flex-col">
        <header className="flex items-center justify-between p-4">
          <button
            onClick={() => setStep('select')}
            className="text-green-500 font-medium"
          >
            Back
          </button>
          <div className="flex items-center gap-2">
            <div className="w-8 h-8 bg-gradient-to-br from-green-400 to-green-600 rounded-full flex items-center justify-center text-black font-bold text-sm">
              {selectedUser?.user_id.slice(0, 1).toUpperCase()}
            </div>
            <span className="font-medium">{selectedUser?.user_id}</span>
          </div>
          <div className="w-16" />
        </header>

        <div className="flex-1 flex flex-col items-center justify-center px-6">
          <div className="text-6xl font-bold mb-4">
            ${amount || '0'}
          </div>
          <p className="text-gray-500">
            Available: ${myBalance.toFixed(2)}
          </p>
        </div>

        {/* Number Pad */}
        <div className="px-6 pb-6">
          <div className="max-w-xs mx-auto grid grid-cols-3 gap-4 mb-6">
            {['1', '2', '3', '4', '5', '6', '7', '8', '9', '.', '0', 'back'].map(
              (key) => (
                <button
                  key={key}
                  onClick={() => handleAmountKey(key)}
                  className="h-16 rounded-full bg-gray-900 hover:bg-gray-800 text-2xl font-medium transition-colors flex items-center justify-center"
                >
                  {key === 'back' ? '←' : key}
                </button>
              )
            )}
          </div>
          <button
            onClick={() => setStep('confirm')}
            disabled={!amount || parseFloat(amount) <= 0}
            className="w-full bg-green-500 hover:bg-green-600 disabled:bg-gray-700 disabled:text-gray-500 text-black font-semibold py-4 rounded-full text-lg transition-colors"
          >
            Continue
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-black text-white flex flex-col">
      <header className="flex items-center justify-between p-4">
        <button
          onClick={() => setStep('amount')}
          className="text-green-500 font-medium"
        >
          Back
        </button>
        <h1 className="text-lg font-semibold">Confirm</h1>
        <div className="w-16" />
      </header>

      <div className="flex-1 flex flex-col items-center justify-center px-6">
        <div className="w-20 h-20 bg-gradient-to-br from-green-400 to-green-600 rounded-full flex items-center justify-center text-black font-bold text-3xl mb-6">
          {selectedUser?.user_id.slice(0, 1).toUpperCase()}
        </div>
        <p className="text-gray-400 mb-2">Sending to</p>
        <p className="text-xl font-semibold mb-8">{selectedUser?.user_id}</p>
        <div className="text-5xl font-bold">${amount}</div>
      </div>

      <div className="px-6 pb-6">
        <button
          onClick={() => transferMutation.mutate()}
          disabled={transferMutation.isPending}
          className="w-full bg-green-500 hover:bg-green-600 disabled:bg-gray-700 text-black font-semibold py-4 rounded-full text-lg transition-colors"
        >
          {transferMutation.isPending ? 'Sending...' : 'Send'}
        </button>
        {transferMutation.isError && (
          <p className="text-red-500 text-center mt-4">
            {(transferMutation.error as Error).message}
          </p>
        )}
      </div>
    </div>
  );
}
