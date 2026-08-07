export function sessionRoute(sessionId: string, workspacePath: string): string {
  const search = new URLSearchParams({ workspace: workspacePath });
  return `/sessions/${encodeURIComponent(sessionId)}?${search.toString()}`;
}
