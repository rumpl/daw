import { useState } from 'react';
import { X } from 'lucide-react';
import type { ContributionContext } from '@/plugin-contributions';
import { closeSessionSideView, usePluginContributions } from '@/plugin-contributions';
import { Button } from '@/components/ui/button';
import { PluginBoundary } from './PluginBoundary';
import { PluginRenderContent } from './PluginRenderContent';

export function SessionSideView({ context }: { context: ContributionContext }) {
  const { sideViews } = usePluginContributions();
  const [width, setWidth] = useState(520);
  const sessionId = context.sessionId;
  const view = sessionId ? sideViews.find((candidate) => candidate.sessionId === sessionId) : undefined;
  if (!sessionId || !view) return null;

  const close = () => closeSessionSideView(sessionId, view.key);
  const beginResize = (event: React.PointerEvent<HTMLDivElement>) => {
    event.preventDefault();
    const pane = event.currentTarget.parentElement;
    if (!pane) return;
    const bounds = pane.getBoundingClientRect();
    const minWidth = Math.min(280, bounds.width * 0.4);
    const maxWidth = Math.max(minWidth, bounds.width - minWidth);
    const update = (pointerEvent: PointerEvent) =>
      setWidth(Math.max(minWidth, Math.min(maxWidth, bounds.right - pointerEvent.clientX)));
    const finish = () => {
      window.removeEventListener('pointermove', update);
      window.removeEventListener('pointerup', finish);
      window.removeEventListener('pointercancel', finish);
      document.body.classList.remove('resizing-side-view');
    };
    document.body.classList.add('resizing-side-view');
    window.addEventListener('pointermove', update);
    window.addEventListener('pointerup', finish, { once: true });
    window.addEventListener('pointercancel', finish, { once: true });
  };
  return (
    <>
    <div className="session-side-view-divider" role="separator" aria-orientation="vertical"
      aria-label="Resize side view" onPointerDown={beginResize} />
    <aside className="session-side-view" aria-label={view.title} style={{ width, flexBasis: width }}>
      <header className="session-side-view-header">
        <strong title={view.title}>{view.title}</strong>
        <Button type="button" size="icon" variant="ghost" aria-label="Close side view" onClick={close}>
          <X aria-hidden="true" />
        </Button>
      </header>
      <div className="session-side-view-content">
        <PluginBoundary message="Plugin side view failed">
          <PluginRenderContent render={() => view.render({ ...context, sessionId, close })} />
        </PluginBoundary>
      </div>
    </aside>
    </>
  );
}
