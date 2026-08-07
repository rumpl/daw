import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from './api';
import type { PluginCatalog } from './protocol.gen';

const emptyCatalog: PluginCatalog = { plugins: [], errors: [] };

export function usePlugins(enabled: boolean) {
  const [catalog, setCatalog] = useState<PluginCatalog>(emptyCatalog);
  const [loadError, setLoadError] = useState('');
  const signature = useRef('');

  const refresh = useCallback(async () => {
    const next = await api.plugins();
    const nextSignature = JSON.stringify(next);
    if (signature.current !== nextSignature) {
      signature.current = nextSignature;
      setCatalog(next);
    }
    setLoadError('');
  }, []);

  useEffect(() => {
    if (!enabled) return;
    void refresh().catch((cause: unknown) => {
      setLoadError(cause instanceof Error ? cause.message : 'plugins could not be loaded');
    });
    const timer = window.setInterval(() => {
      void refresh().catch(() => undefined);
    }, 3_000);
    return () => window.clearInterval(timer);
  }, [enabled, refresh]);

  return { catalog, loadError, refresh };
}
