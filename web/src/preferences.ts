import { useCallback, useMemo, useState } from 'react';
import type { Bootstrap } from './protocol.gen';
import type { SendMode } from './components/Composer';

const LS_PREFS = 'dawui.prefs.v1';

export interface Prefs {
  /** Most-recent-first list of workspace paths that opened successfully. */
  recentWorkspaces: string[];
  deliveryMode: SendMode;
}

export function loadPrefs(): Prefs {
  try {
    const raw = localStorage.getItem(LS_PREFS);
    if (!raw) return { recentWorkspaces: [], deliveryMode: 'normal' };
    const parsed = JSON.parse(raw) as Partial<Prefs>;
    return {
      recentWorkspaces: (parsed.recentWorkspaces ?? []).filter((path) => typeof path === 'string').slice(0, 8),
      deliveryMode: parsed.deliveryMode ?? 'normal',
    };
  } catch {
    return { recentWorkspaces: [], deliveryMode: 'normal' };
  }
}

function savePrefs(prefs: Prefs) {
  try {
    localStorage.setItem(LS_PREFS, JSON.stringify(prefs));
  } catch {
    /* Storage is optional. */
  }
}

export function useWorkspacePreferences(boot: Bootstrap | null) {
  const [prefs, setPrefs] = useState<Prefs>(loadPrefs);

  const recentWorkspaces = useMemo(() => {
    const serverPaths = (boot?.workspaceHints ?? []).map((hint) => hint.path);
    return [...new Set([...prefs.recentWorkspaces, ...serverPaths])].slice(0, 10);
  }, [boot?.workspaceHints, prefs.recentWorkspaces]);

  const rememberWorkspace = useCallback((path: string) => {
    setPrefs((current) => {
      const next = {
        ...current,
        recentWorkspaces: [path, ...current.recentWorkspaces.filter((item) => item !== path)].slice(0, 8),
      };
      savePrefs(next);
      return next;
    });
  }, []);

  const forgetWorkspace = useCallback((path: string) => {
    setPrefs((current) => {
      const next = { ...current, recentWorkspaces: current.recentWorkspaces.filter((item) => item !== path) };
      savePrefs(next);
      return next;
    });
  }, []);

  return { prefs, recentWorkspaces, rememberWorkspace, forgetWorkspace };
}
