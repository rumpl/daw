import { useState, type DragEvent } from 'react';
import type { SessionSummary } from '../protocol.gen';
import { clip } from '../safety';

interface SessionTabsProps {
  sessions: SessionSummary[];
  activeSessionId: string | null;
  busy: boolean;
  canCreateChat: boolean;
  onNewChat: () => void;
  onOpen: (sessionId: string, workspacePath: string) => void;
  onClose: (sessionId: string, chatId: string) => void;
  onReorder: (draggedSessionId: string, targetSessionId: string) => void;
}

export function SessionTabs({
  sessions,
  activeSessionId,
  busy,
  canCreateChat,
  onNewChat,
  onOpen,
  onClose,
  onReorder,
}: SessionTabsProps) {
  const [draggedSessionId, setDraggedSessionId] = useState<string | null>(null);
  const [dropTargetId, setDropTargetId] = useState<string | null>(null);

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
    <nav className="session-tabs" aria-label="Chat tabs">
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
    </nav>
  );
}
