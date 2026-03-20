import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { useAPI, useAuth } from '../hooks/useAPI';

export function Home() {
  const api = useAPI();
  const { user, logout, setToken } = useAuth();
  const queryClient = useQueryClient();
  const [showUserMenu, setShowUserMenu] = useState(false);
  const [newUsername, setNewUsername] = useState('');

  const { data: accounts, isLoading } = useQuery({
    queryKey: ['accounts'],
    queryFn: () => api.getAccounts(),
  });

  // Fetch all users for the dev mode switcher
  const { data: allUsers } = useQuery({
    queryKey: ['users'],
    queryFn: () => api.getAllUsers(),
    enabled: user?.dev_mode === true,
  });

  const primaryAccount = accounts?.[0];
  const balance = primaryAccount ? parseFloat(primaryAccount.balance) : 0;
  const username = user?.username || 'User';

  const handleSwitchUser = () => {
    if (newUsername.trim()) {
      setToken(`dev_${newUsername.trim()}`);
      setNewUsername('');
      setShowUserMenu(false);
      queryClient.invalidateQueries();
      window.location.reload();
    }
  };

  return (
    <div className="min-h-screen bg-black text-white flex flex-col">
      {/* Header */}
      <header className="flex items-center justify-between p-4">
        <button
          onClick={() => setShowUserMenu(!showUserMenu)}
          className="flex items-center gap-2"
        >
          <div className="w-10 h-10 bg-gradient-to-br from-green-400 to-green-600 rounded-full flex items-center justify-center text-black font-bold">
            {username.slice(0, 1).toUpperCase()}
          </div>
          <span className="font-medium">{username}</span>
          <svg
            className="w-4 h-4 text-gray-400"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              d="M19 9l-7 7-7-7"
            />
          </svg>
        </button>
        <Link
          to="/intern/admin"
          className="text-gray-500 hover:text-white text-sm"
        >
          Admin
        </Link>
      </header>

      {/* User switcher dropdown */}
      {showUserMenu && (
        <div className="absolute top-16 left-4 bg-gray-900 rounded-xl p-4 shadow-xl z-50 w-64">
          {user?.dev_mode && (
            <>
              <p className="text-sm text-gray-400 mb-2">Switch user (dev mode)</p>
              <div className="flex gap-2 mb-3">
                <input
                  type="text"
                  value={newUsername}
                  onChange={(e) => setNewUsername(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && handleSwitchUser()}
                  placeholder="Username"
                  className="flex-1 bg-gray-800 text-white px-3 py-2 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-green-500"
                />
                <button
                  onClick={handleSwitchUser}
                  className="bg-green-500 text-black px-3 py-2 rounded-lg text-sm font-medium"
                >
                  Go
                </button>
              </div>
              <div className="space-y-1 mb-3 max-h-48 overflow-y-auto">
                {allUsers?.map((u) => (
                  <button
                    key={u.id}
                    onClick={() => {
                      setToken(`dev_${u.user_id}`);
                      setShowUserMenu(false);
                      queryClient.invalidateQueries();
                      window.location.reload();
                    }}
                    className={`w-full text-left px-3 py-2 text-sm hover:bg-gray-800 rounded-lg flex justify-between items-center ${
                      u.user_id === user?.id ? 'text-green-400' : 'text-gray-300'
                    }`}
                  >
                    <span>{u.user_id}</span>
                    <span className="text-gray-500 text-xs">${parseFloat(u.balance).toFixed(2)}</span>
                  </button>
                ))}
                {!allUsers?.length && (
                  <p className="text-gray-500 text-sm px-3 py-2">No users yet</p>
                )}
              </div>
              <hr className="border-gray-700 mb-3" />
            </>
          )}
          <button
            onClick={logout}
            className="w-full text-left px-3 py-2 text-sm text-red-400 hover:bg-gray-800 rounded-lg"
          >
            Sign out
          </button>
        </div>
      )}

      {/* Balance Section */}
      <div className="flex-1 flex flex-col items-center justify-center px-6">
        <p className="text-gray-400 text-sm mb-2">Your Balance</p>
        {isLoading ? (
          <div className="text-6xl font-bold">...</div>
        ) : (
          <div className="text-6xl md:text-8xl font-bold tracking-tight">
            ${balance.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
          </div>
        )}
        {primaryAccount && (
          <p className="text-gray-500 text-sm mt-4 font-mono">
            {primaryAccount.currency}
          </p>
        )}
      </div>

      {/* Action Buttons */}
      <div className="px-6 pb-12">
        <div className="max-w-md mx-auto grid grid-cols-2 gap-4">
          <Link
            to="/send"
            className="bg-green-500 hover:bg-green-600 text-black font-semibold py-4 px-6 rounded-full text-center text-lg transition-colors"
          >
            Send
          </Link>
          <Link
            to="/activity"
            className="bg-gray-800 hover:bg-gray-700 text-white font-semibold py-4 px-6 rounded-full text-center text-lg transition-colors"
          >
            Activity
          </Link>
        </div>
      </div>
    </div>
  );
}
