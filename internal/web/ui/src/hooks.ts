import { useCallback, useEffect, useState } from 'react';
import { api, ApiError } from './api';

// useData reads one of the back end's read-only views.
export function useData<T>(name: string, refreshKey = 0) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);
  const reload = useCallback(() => setTick((t) => t + 1), []);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    api
      .get<T>(`/api/data/${name}`)
      .then((d) => {
        if (!alive) return;
        setData(d);
        setError(null);
      })
      .catch((e: ApiError) => {
        if (!alive) return;
        setError(e.status === 409 ? 'Another operation is running; this will refresh when it finishes.' : e.message);
      })
      .finally(() => alive && setLoading(false));
    return () => {
      alive = false;
    };
  }, [name, tick, refreshKey]);

  return { data, error, loading, reload };
}
