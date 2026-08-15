import { TooltipProvider } from '@/components/ui/tooltip';
import { loadPrefs, type ThemeMode } from '@/preferences';
import { useEffect, useState, type ReactNode } from 'react';

export const THEME_CHANGE_EVENT = 'dawui:theme-change';

function resolvedDark(mode: ThemeMode, systemDark: boolean) {
  return mode === 'dark' || (mode === 'system' && systemDark);
}

export function AppTheme({ children }: { children: ReactNode }) {
  const [mode, setMode] = useState<ThemeMode>(() => loadPrefs().theme);
  const [systemDark, setSystemDark] = useState(() => window.matchMedia('(prefers-color-scheme: dark)').matches);
  const dark = resolvedDark(mode, systemDark);

  useEffect(() => {
    const media = window.matchMedia('(prefers-color-scheme: dark)');
    const updateSystem = () => setSystemDark(media.matches);
    const updateMode = () => setMode(loadPrefs().theme);
    media.addEventListener('change', updateSystem);
    window.addEventListener(THEME_CHANGE_EVENT, updateMode);
    return () => {
      media.removeEventListener('change', updateSystem);
      window.removeEventListener(THEME_CHANGE_EVENT, updateMode);
    };
  }, []);

  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark);
    document.documentElement.style.colorScheme = dark ? 'dark' : 'light';
  }, [dark]);

  return <div className={dark ? 'dark h-full' : 'h-full'}><TooltipProvider>{children}</TooltipProvider></div>;
}
