export function pluginRoute(pluginId: string, path = ''): string {
  const suffix = path
    .split('/')
    .filter(Boolean)
    .map((part) => encodeURIComponent(part))
    .join('/');
  return `/plugins/${encodeURIComponent(pluginId)}${suffix ? `/${suffix}` : ''}`;
}

export function sessionRoute(sessionId: string, workspacePath: string): string {
  const search = new URLSearchParams({ workspace: workspacePath });
  return `/sessions/${encodeURIComponent(sessionId)}?${search.toString()}`;
}
