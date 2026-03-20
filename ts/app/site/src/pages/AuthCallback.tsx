import { useEffect } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useAuth } from '../hooks/useAPI';

export function AuthCallback() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const { checkAuth } = useAuth();

  useEffect(() => {
    const success = searchParams.get('success');

    if (success === 'true') {
      // Auth was successful, check current user
      checkAuth().then(() => {
        navigate('/');
      });
    } else {
      // Auth failed
      navigate('/login');
    }
  }, [searchParams, navigate, checkAuth]);

  return (
    <div className="min-h-screen bg-black text-white flex items-center justify-center">
      <div className="text-center">
        <div className="w-8 h-8 border-2 border-green-500 border-t-transparent rounded-full animate-spin mx-auto mb-4" />
        <p className="text-gray-400">Signing you in...</p>
      </div>
    </div>
  );
}
