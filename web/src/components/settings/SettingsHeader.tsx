import { Button } from '@/components/ui/button';
import { Menu, X } from 'lucide-react';
import type { RefObject } from 'react';

interface SettingsHeaderProps {
  title: string;
  menuButton: RefObject<HTMLButtonElement | null>;
  drawerOpen: boolean;
  onToggleDrawer: () => void;
  onClose: () => void;
}

export function SettingsHeader({ title, menuButton, drawerOpen, onToggleDrawer, onClose }: SettingsHeaderProps) {
  return (
    <header className="topbar">
      <Button ref={menuButton} type="button" variant="secondary" className="menu-button"
        aria-expanded={drawerOpen} aria-controls="sidebar" onClick={onToggleDrawer}>
        <Menu aria-hidden="true" /> Menu
      </Button>
      <div className="topbar-title"><h1>{title}</h1></div>
      <Button type="button" size="icon-sm" variant="ghost" aria-label="Close settings" onClick={onClose}>
        <X aria-hidden="true" />
      </Button>
    </header>
  );
}
