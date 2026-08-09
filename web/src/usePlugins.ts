import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from './api';
import type { PluginCatalog } from './protocol.gen';

const emptyCatalog: PluginCatalog = { plugins: [], errors: [] };

export function usePlugins(enabled: boolean, revision = 0) {
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
  }, [enabled, refresh, revision]);

  return { catalog, loadError, refresh };
}
