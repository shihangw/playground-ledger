import { createContext, useContext, useMemo, useState, useEffect, useCallback } from 'react';
import type { ReactNode } from 'react';
import { createAPI } from '../lib/api';

type APIContextType = ReturnType<typeof createAPI>;

interface User {
  id: string;
  email: string;
  username: string;
  dev_mode: boolean;
}

interface AuthContextType {
  api: APIContextType;
  token: string | null;
  setToken: (token: string) => void;
  user: User | null;
  isLoading: boolean;
  isAuthenticated: boolean;
  devMode: boolean;
  setDevMode: (mode: boolean) => void;
  logout: () => Promise<void>;
  checkAuth: () => Promise<void>;
}

const AuthContext = createContext<AuthContextType | null>(null);

export function APIProvider({ children }: { children: ReactNode }) {
  const [token, setTokenState] = useState<string | null>(() => {
    return localStorage.getItem('dev_token');
  });
  const [user, setUser] = useState<User | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [devMode, setDevModeState] = useState(() => {
    return localStorage.getItem('dev_mode') === 'true';
  });

  const setToken = (newToken: string) => {
    setTokenState(newToken);
    localStorage.setItem('dev_token', newToken);
  };

  const setDevMode = (mode: boolean) => {
    setDevModeState(mode);
    localStorage.setItem('dev_mode', String(mode));
  };

  const api = useMemo(() => createAPI(token || ''), [token]);

  const checkAuth = useCallback(async () => {
    setIsLoading(true);
    try {
      const response = await fetch('/auth/me', {
        credentials: 'include',
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });

      if (response.ok) {
        const userData = await response.json();
        setUser(userData);
      } else {
        setUser(null);
        // Clear invalid token
        if (token && !token.startsWith('dev_')) {
          setTokenState(null);
          localStorage.removeItem('dev_token');
        }
      }
    } catch {
      setUser(null);
    } finally {
      setIsLoading(false);
    }
  }, [token]);

  const logout = async () => {
    try {
      await fetch('/auth/logout', {
        method: 'POST',
        credentials: 'include',
      });
    } catch {
      // Ignore errors
    }
    setUser(null);
    setTokenState(null);
    localStorage.removeItem('dev_token');
    window.location.href = '/login';
  };

  // Check auth on mount
  useEffect(() => {
    checkAuth();
  }, [checkAuth]);

  const isAuthenticated = !!user;

  return (
    <AuthContext.Provider
      value={{
        api,
        token,
        setToken,
        user,
        isLoading,
        isAuthenticated,
        devMode,
        setDevMode,
        logout,
        checkAuth,
      }}
    >
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  const context = useContext(AuthContext);
  if (!context) {
    throw new Error('useAuth must be used within APIProvider');
  }
  return context;
}

export function useAPI(): APIContextType {
  const { api } = useAuth();
  return api;
}
