import { useState, type DragEvent } from 'react';
import type { Plugin, SessionSummary } from '../protocol.gen';
import { clip } from '../safety';
import { usePluginContributions } from '../plugin-contributions';
import { PluginSlotView } from './PluginSurfaces';

interface PluginTab {
  plugin: Plugin;
  path: string;
}

interface SessionTabsProps {
  sessions: SessionSummary[];
  activeSessionId: string | null;
  plugins?: PluginTab[];
  activePluginId?: string | null;
  busy: boolean;
  canCreateChat: boolean;
  onNewChat: () => void;
  onOpen: (sessionId: string, workspacePath: string) => void;
  onClose: (sessionId: string, chatId: string) => void;
  onReorder: (draggedSessionId: string, targetSessionId: string) => void;
  onOpenPlugin?: (pluginId: string, path: string) => void;
  onClosePlugin?: (pluginId: string) => void;
  onSplit?: (sessionId: string, workspacePath: string, direction: 'vertical' | 'horizontal') => void;
}

export function SessionTabs({
  sessions,
  activeSessionId,
  plugins = [],
  activePluginId = null,
  busy,
  canCreateChat,
  onNewChat,
  onOpen,
  onClose,
  onReorder,
  onOpenPlugin,
  onClosePlugin,
  onSplit,
}: SessionTabsProps) {
  const [draggedSessionId, setDraggedSessionId] = useState<string | null>(null);
  const [dropTargetId, setDropTargetId] = useState<string | null>(null);
  const { badges } = usePluginContributions();

  const finishDrag = () => {
    setDraggedSessionId(null);
    setDropTargetId(null);
  };

  const dropTab = (event: DragEvent<HTMLDivElement>, targetSessionId: string) => {
    event.preventDefault();
    if (draggedSessionId && draggedSessionId !== targetSessionId) {
      onReorder(draggedSessionId, targetSessionId);
    }
    finishDrag();
  };

  return (
    <nav className="session-tabs" aria-label="Open tabs">
      {sessions.map((session) => {
        const title = session.title || 'Untitled';
        const active = session.sessionId === activeSessionId;
        const runStatus = session.runState === 'running'
          ? 'Running'
          : session.runState === 'stopping'
            ? 'Stopping'
            : 'Not running';
        return (
          <div
            className="session-tab"
            data-active={active || undefined}
            data-dragging={draggedSessionId === session.sessionId || undefined}
            data-drop-target={dropTargetId === session.sessionId || undefined}
            draggable={!busy}
            key={session.sessionId}
            onDragStart={(event) => {
              setDraggedSessionId(session.sessionId);
              event.dataTransfer.effectAllowed = 'move';
              event.dataTransfer.setData('text/plain', session.sessionId);
            }}
            onDragOver={(event) => {
              if (!draggedSessionId || draggedSessionId === session.sessionId) return;
              event.preventDefault();
              event.dataTransfer.dropEffect = 'move';
              setDropTargetId(session.sessionId);
            }}
            onDragLeave={() => setDropTargetId((current) => current === session.sessionId ? null : current)}
            onDrop={(event) => dropTab(event, session.sessionId)}
            onDragEnd={finishDrag}
          >
            <button
              type="button"
              className="session-tab-open"
              data-running={session.runState === 'running' || undefined}
              aria-current={active ? 'page' : undefined}
              title={`${title} — ${runStatus} — ${session.workingDir}`}
              aria-label={`${title} — ${runStatus}`}
              onClick={() => {
                if (!active) onOpen(session.sessionId, session.workingDir);
              }}
              disabled={busy}
            >
              <span>{clip(title, 50)}</span>
              {session.runState === 'running' ? (
                <span className="session-tab-status run-dot run-running" aria-hidden="true" />
              ) : null}
              {badges.filter((badge) => badge.sessionId === session.sessionId).map((badge) => (
                <span className={`plugin-session-badge plugin-session-badge-${badge.tone}`} key={badge._key}>{badge.value}</span>
              ))}
              <PluginSlotView slot="session-tab.badge" context={{workspace: null, chatId: session.chatId ?? null, session: null, sessionId: session.sessionId}} />
            </button>
            <button
              type="button"
              className="session-tab-close"
              aria-label={`Close live session ${title}`}
              title="Close session"
              onClick={() => onClose(session.sessionId, session.chatId ?? '')}
              disabled={busy || !session.chatId}
            >
              ×
            </button>
          </div>
        );
      })}
      {plugins.map(({ plugin, path }) => {
        const active = plugin.id === activePluginId;
        return (
          <div className="session-tab plugin-tab" data-active={active || undefined} key={`plugin:${plugin.id}`}>
            <button
              type="button"
              className="session-tab-open"
              aria-current={active ? 'page' : undefined}
              title={plugin.description || plugin.name}
              aria-label={`${plugin.name} plugin`}
              onClick={() => {
                if (!active) onOpenPlugin?.(plugin.id, path);
              }}
            >
              <span>{clip(plugin.name, 50)}</span>
            </button>
            <button
              type="button"
              className="session-tab-close"
              aria-label={`Close plugin ${plugin.name}`}
              title="Close plugin"
              onClick={() => onClosePlugin?.(plugin.id)}
            >
              ×
            </button>
          </div>
        );
      })}
      <button
        type="button"
        className="session-tab-new"
        aria-label="Create new chat"
        title="New chat"
        onClick={onNewChat}
        disabled={busy || !canCreateChat}
      >
        +
      </button>
      {onSplit && activeSessionId ? (
        <div className="session-split-actions" aria-label="Split editor">
          <button
            type="button"
            aria-label="Split active tab vertically"
            title="Split vertically"
            onClick={() => {
              const active = sessions.find((item) => item.sessionId === activeSessionId);
              if (active) onSplit(active.sessionId, active.workingDir, 'vertical');
            }}
            disabled={busy}
          >
            <span className="split-icon split-icon-vertical" aria-hidden="true" />
          </button>
          <button
            type="button"
            aria-label="Split active tab horizontally"
            title="Split horizontally"
            onClick={() => {
              const active = sessions.find((item) => item.sessionId === activeSessionId);
              if (active) onSplit(active.sessionId, active.workingDir, 'horizontal');
            }}
            disabled={busy}
          >
            <span className="split-icon split-icon-horizontal" aria-hidden="true" />
          </button>
        </div>
      ) : null}
    </nav>
  );
}
