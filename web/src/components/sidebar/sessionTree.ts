import type { SessionSummary } from '@/protocol.gen';

export interface SessionNode {
  session: SessionSummary;
  children: SessionNode[];
}

function sessionDay(createdAt: string): string {
  const date = new Date(createdAt);
  if (Number.isNaN(date.getTime())) return 'Unknown date';
  const today = new Date();
  const startOfToday = new Date(today.getFullYear(), today.getMonth(), today.getDate());
  const startOfDate = new Date(date.getFullYear(), date.getMonth(), date.getDate());
  const daysAgo = Math.round((startOfToday.getTime() - startOfDate.getTime()) / 86_400_000);
  if (daysAgo === 0) return 'Today';
  if (daysAgo === 1) return 'Yesterday';
  return new Intl.DateTimeFormat(undefined, {
    weekday: 'long',
    month: 'short',
    day: 'numeric',
    year: date.getFullYear() === today.getFullYear() ? undefined : 'numeric',
  }).format(date);
}

function sessionForest(sessions: SessionSummary[]): SessionNode[] {
  const nodes = new Map(sessions.map((session) => [session.sessionId, { session, children: [] as SessionNode[] }]));
  const roots: SessionNode[] = [];
  for (const node of nodes.values()) {
    const parent = node.session.parentSessionId ? nodes.get(node.session.parentSessionId) : undefined;
    if (parent && parent !== node) parent.children.push(node);
    else roots.push(node);
  }
  const sort = (items: SessionNode[]) => {
    items.sort((a, b) => b.session.createdAt.localeCompare(a.session.createdAt));
    items.forEach((item) => sort(item.children));
  };
  sort(roots);
  return roots;
}

export function groupSessionsByDay(sessions: SessionSummary[]) {
  const groups = new Map<string, SessionNode[]>();
  for (const node of sessionForest(sessions)) {
    const label = sessionDay(node.session.createdAt);
    const group = groups.get(label);
    if (group) group.push(node);
    else groups.set(label, [node]);
  }
  return Array.from(groups, ([label, groupedSessions]) => ({ label, sessions: groupedSessions }));
}
