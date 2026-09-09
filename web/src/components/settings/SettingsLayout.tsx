import { NavLink } from 'react-router-dom';
import type { ReactNode } from 'react';

export function SettingsLayout({ children }: { children: ReactNode }) {
  return (
    <div className="settings-layout">
      <nav className="settings-navigation" aria-label="Settings sections">
        <NavLink to="/settings" end>General</NavLink>
        <NavLink to="/settings/projects">Projects</NavLink>
        <NavLink to="/settings/plugins">Plugins</NavLink>
      </nav>
      <div className="settings-content">{children}</div>
    </div>
  );
}
