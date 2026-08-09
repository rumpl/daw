import { Component, useEffect, useMemo, useState, type ReactNode } from 'react';
import type { ContributionContext, PluginActionLocation, PluginSlot } from '../plugin-contributions';
import { dismissNotification, usePluginContributions } from '../plugin-contributions';

class PluginBoundary extends Component<{children: ReactNode}, {failed: boolean}> {
  override state = {failed: false};
  static getDerivedStateFromError() { return {failed: true}; }
  override render(): ReactNode {
    if (this.state.failed) return <span className="plugin-contribution-error">Plugin contribution failed</span>;
    return this.props.children;
  }
}

export function PluginSlotView({slot, context, className = ''}: {
  slot: PluginSlot;
  context: ContributionContext;
  className?: string;
}) {
  const {slots} = usePluginContributions();
  const matching = slots.filter((item) => item.slot === slot);
  if (matching.length === 0) return null;
  return (
    <div className={`plugin-slot plugin-slot-${slot.replaceAll('.', '-')} ${className}`.trim()}>
      {matching.map((item) => <PluginBoundary key={item._key}>{item.render(context)}</PluginBoundary>)}
    </div>
  );
}

export function PluginActionButtons({location, context}: {
  location: PluginActionLocation;
  context: ContributionContext;
}) {
  const {actions} = usePluginContributions();
  const matching = actions.filter((action) => action.locations.includes(location) && (!action.when || action.when(context)));
  if (matching.length === 0) return null;
  return (
    <div className={`plugin-actions plugin-actions-${location}`}>
      {matching.map((action) => (
        <button type="button" key={action._key} title={action.description || action.label}
          onClick={() => void action.run(context)}>{action.label}</button>
      ))}
    </div>
  );
}

export function PluginNotifications() {
  const {notifications} = usePluginContributions();
  if (notifications.length === 0) return null;
  return (
    <aside className="plugin-notifications" aria-label="Plugin notifications" aria-live="polite">
      {notifications.map((notification) => (
        <section className={`plugin-notification plugin-notification-${notification.level}`} key={notification.key}>
          <div><strong>{notification.title}</strong>{notification.message ? <p>{notification.message}</p> : null}</div>
          <button type="button" aria-label={`Dismiss ${notification.title}`} onClick={() => dismissNotification(notification.key)}>×</button>
        </section>
      ))}
    </aside>
  );
}

export function PluginCommandPalette({context}: {context: ContributionContext}) {
  const {actions} = usePluginContributions();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        setOpen((value) => !value);
      } else if (event.key === 'Escape') setOpen(false);
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, []);
  const matches = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return actions.filter((action) => action.locations.includes('command-palette') &&
      (!action.when || action.when(context)) &&
      (!needle || action.label.toLowerCase().includes(needle) || action.description?.toLowerCase().includes(needle)));
  }, [actions, context, query]);
  if (!open) return null;
  return (
    <div className="dialog-scrim plugin-command-scrim" role="presentation" onMouseDown={() => setOpen(false)}>
      <section className="dialog plugin-command-palette" role="dialog" aria-modal="true" aria-label="Command palette"
        onMouseDown={(event) => event.stopPropagation()}>
        <input autoFocus value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search plugin actions…" />
        <ul>
          {matches.map((action) => <li key={action._key}><button type="button" onClick={() => {
            setOpen(false);
            void action.run(context);
          }}><strong>{action.label}</strong>{action.description ? <span>{action.description}</span> : null}</button></li>)}
        </ul>
        {matches.length === 0 ? <p className="hint">No matching plugin actions.</p> : null}
      </section>
    </div>
  );
}
