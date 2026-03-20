import { useState } from 'react';
import { useAuth } from '../hooks/useAPI';

export function Login() {
  const { setToken, devMode, setDevMode } = useAuth();
  const [username, setUsername] = useState('');

  const handleWorkOSLogin = () => {
    // Redirect to backend auth endpoint
    window.location.href = '/auth/login';
  };

  const handleDevLogin = () => {
    if (username.trim()) {
      setToken(`dev_${username.trim()}`);
      window.location.href = '/';
    }
  };

  return (
    <div className="min-h-screen bg-black text-white flex flex-col items-center justify-center px-6">
      <div className="w-full max-w-sm">
        <h1 className="text-4xl font-bold text-center mb-2">Playground</h1>
        <p className="text-gray-400 text-center mb-12">Ledger</p>

        {devMode ? (
          <>
            <div className="space-y-4">
              <input
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && handleDevLogin()}
                placeholder="Enter username"
                className="w-full bg-gray-900 text-white px-4 py-3 rounded-xl focus:outline-none focus:ring-2 focus:ring-green-500 text-center"
                autoFocus
              />
              <button
                onClick={handleDevLogin}
                disabled={!username.trim()}
                className="w-full bg-green-500 hover:bg-green-600 disabled:bg-gray-700 disabled:text-gray-500 text-black font-semibold py-4 rounded-full text-lg transition-colors"
              >
                Continue as {username || '...'}
              </button>
            </div>

            <div className="mt-6 space-y-2">
              <p className="text-gray-500 text-sm text-center">Quick login:</p>
              <div className="flex gap-2 justify-center">
                {['alice', 'bob', 'charlie'].map((name) => (
                  <button
                    key={name}
                    onClick={() => {
                      setToken(`dev_${name}`);
                      window.location.href = '/';
                    }}
                    className="px-4 py-2 bg-gray-800 hover:bg-gray-700 rounded-full text-sm"
                  >
                    {name}
                  </button>
                ))}
              </div>
            </div>

            <button
              onClick={() => setDevMode(false)}
              className="mt-8 w-full text-gray-500 hover:text-white text-sm"
            >
              Use WorkOS instead
            </button>
          </>
        ) : (
          <>
            <button
              onClick={handleWorkOSLogin}
              className="w-full bg-green-500 hover:bg-green-600 text-black font-semibold py-4 rounded-full text-lg transition-colors"
            >
              Sign in with WorkOS
            </button>

            <button
              onClick={() => setDevMode(true)}
              className="mt-4 w-full text-gray-500 hover:text-white text-sm"
            >
              Use dev mode instead
            </button>
          </>
        )}
      </div>
    </div>
  );
}
