import { TooltipProvider } from '@/components/ui/tooltip';
import { useEffect, useState, type ReactNode } from 'react';

export function AppTheme({ children }: { children: ReactNode }) {
  const [dark, setDark] = useState(() => window.matchMedia('(prefers-color-scheme: dark)').matches);

  useEffect(() => {
    const media = window.matchMedia('(prefers-color-scheme: dark)');
    const update = () => {
      setDark(media.matches);
      document.documentElement.classList.toggle('dark', media.matches);
    };
    update();
    media.addEventListener('change', update);
    return () => media.removeEventListener('change', update);
  }, []);

  return <div className={dark ? 'dark h-full' : 'h-full'}><TooltipProvider>{children}</TooltipProvider></div>;
}
