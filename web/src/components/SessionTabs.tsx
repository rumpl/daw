import type { SessionSummary } from '../protocol.gen';
import { clip } from '../safety';

interface SessionTabsProps {
  sessions: SessionSummary[];
  activeSessionId: string | null;
  busy: boolean;
  onOpen: (sessionId: string, workspacePath: string) => void;
  onClose: (sessionId: string, chatId: string) => void;
}

export function SessionTabs({ sessions, activeSessionId, busy, onOpen, onClose }: SessionTabsProps) {
  if (sessions.length === 0) return null;

  return (
    <nav className="session-tabs" aria-label="Live sessions">
      {sessions.map((session) => {
        const title = session.title || 'Untitled';
        const active = session.sessionId === activeSessionId;
        const runStatus = session.runState === 'running'
          ? 'Running'
          : session.runState === 'stopping'
            ? 'Stopping'
            : 'Not running';
        return (
          <div className="session-tab" data-active={active || undefined} key={session.sessionId}>
            <button
              type="button"
              className="session-tab-open"
              aria-current={active ? 'page' : undefined}
              title={`${title} — ${runStatus} — ${session.workingDir}`}
              aria-label={`${title} — ${runStatus}`}
              onClick={() => onOpen(session.sessionId, session.workingDir)}
              disabled={busy || active}
            >
              <span className={`run-dot run-${session.runState ?? 'idle'}`} aria-hidden="true" />
              <span>{clip(title, 50)}</span>
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
    </nav>
  );
}
