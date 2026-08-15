import { ChatPane } from '@/components/chat/ChatPane';
import { SessionTabs } from '@/components/sessions/SessionTabs';
import { useDashboard } from '@/hooks/useDashboard';
import { useRef } from 'react';
import type { SplitPaneState } from './paneLayout';

interface SplitSessionPaneProps {
  pane: SplitPaneState;
  sessionsRevision: number;
  onClose: () => void;
  onSplit: (sessionId: string, workspacePath: string, direction: 'vertical' | 'horizontal') => void;
}

export function SplitSessionPane({ pane, sessionsRevision, onClose, onSplit }: SplitSessionPaneProps) {
  const menuButton = useRef<HTMLButtonElement | null>(null);
  const dashboard = useDashboard({
    sessionId: pane.sessionId,
    workspacePath: pane.workspacePath,
    openSession: () => undefined,
    leaveSession: () => undefined,
  }, sessionsRevision);
  const session = dashboard.liveSessions.find((candidate) => candidate.sessionId === pane.sessionId);

  return (
    <section className="main-pane">
      <SessionTabs
        sessions={session ? [session] : []} activeSessionId={pane.sessionId} busy={dashboard.busyAction}
        canCreateChat={false} onNewChat={() => undefined} onOpen={() => undefined}
        onClose={onClose} onReorder={() => undefined} onSplit={onSplit}
      />
      <ChatPane dashboard={dashboard} menuButton={menuButton} showMenu={false} />
    </section>
  );
}
