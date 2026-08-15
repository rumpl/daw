import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible';
import { clip } from '@/safety';
import { ChevronRight } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import type { SessionNode } from './sessionTree';

export function SessionTreeItem({ node, busy, activeSessionId, onResumeChat }: {
  node: SessionNode;
  busy: boolean;
  activeSessionId: string | null;
  onResumeChat: (sessionId: string) => void;
}) {
  const session = node.session;
  const hasChildren = node.children.length > 0;
  const active = session.sessionId === activeSessionId;
  const hasActiveDescendant = node.children.some((child) => containsSession(child, activeSessionId));
  const [open, setOpen] = useState(true);
  const buttonRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (hasActiveDescendant) setOpen(true);
  }, [hasActiveDescendant]);

  useEffect(() => {
    if (active) buttonRef.current?.scrollIntoView({ block: 'nearest' });
  }, [active]);

  return (
    <li role="treeitem" aria-expanded={hasChildren ? open : undefined}>
      <Collapsible open={open} onOpenChange={setOpen} disabled={!hasChildren}>
        <div className="session-tree-row">
          {hasChildren ? (
            <CollapsibleTrigger render={
              <Button type="button" size="icon-xs" variant="ghost" className="session-tree-toggle"
                aria-label={`Toggle replies to ${session.title || 'Untitled'}`}>
                <ChevronRight aria-hidden="true" />
              </Button>
            } />
          ) : <span className="session-tree-spacer" />}
          <Button ref={buttonRef} type="button" variant="ghost" className="session-tree-open"
            aria-current={active ? 'page' : undefined}
            title={session.title || 'Untitled'} onClick={() => onResumeChat(session.sessionId)} disabled={busy}>
            <span className="session-title">
              <span>{clip(session.title || 'Untitled', 80)}</span>
              {session.runState === 'running' ? (
                <Badge variant="secondary" aria-label="Running"><span className="run-dot run-running" aria-hidden="true" /></Badge>
              ) : null}
            </span>
          </Button>
        </div>
        {hasChildren ? (
          <CollapsibleContent>
            <ul role="group">
              {node.children.map((child) => (
                <SessionTreeItem key={child.session.sessionId} node={child} busy={busy}
                  activeSessionId={activeSessionId} onResumeChat={onResumeChat} />
              ))}
            </ul>
          </CollapsibleContent>
        ) : null}
      </Collapsible>
    </li>
  );
}

function containsSession(node: SessionNode, sessionId: string | null): boolean {
  return Boolean(sessionId) && (
    node.session.sessionId === sessionId || node.children.some((child) => containsSession(child, sessionId))
  );
}
