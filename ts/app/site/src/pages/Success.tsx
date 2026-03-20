import { useLocation, useNavigate } from 'react-router-dom';
import type { Account } from '../lib/api';

export function Success() {
  const navigate = useNavigate();
  const location = useLocation();
  const { amount, recipient } = (location.state || {}) as {
    amount?: string;
    recipient?: Account;
  };

  return (
    <div className="min-h-screen bg-black text-white flex flex-col items-center justify-center px-6">
      <div className="w-24 h-24 bg-green-500 rounded-full flex items-center justify-center mb-8">
        <svg
          className="w-12 h-12 text-black"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
        >
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            strokeWidth={3}
            d="M5 13l4 4L19 7"
          />
        </svg>
      </div>

      <h1 className="text-2xl font-bold mb-2">Sent!</h1>

      {amount && recipient && (
        <p className="text-gray-400 text-center">
          ${amount} sent to {recipient.user_id}
        </p>
      )}

      <button
        onClick={() => navigate('/')}
        className="mt-12 bg-gray-800 hover:bg-gray-700 text-white font-semibold py-4 px-12 rounded-full text-lg transition-colors"
      >
        Done
      </button>
    </div>
  );
}
