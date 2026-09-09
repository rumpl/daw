const OPEN_SESSION_TABS_KEY = 'dawui.open-session-tabs.v1';
const LEGACY_CLOSED_SESSION_TABS_KEY = 'dawui.closed-session-tabs.v1';

export function loadOpenSessionTabIds(): string[] {
  try {
    const value: unknown = JSON.parse(window.localStorage.getItem(OPEN_SESSION_TABS_KEY) ?? '[]');
    if (!Array.isArray(value)) return [];
    return [...new Set(value.filter((id): id is string => typeof id === 'string' && id.length > 0))];
  } catch {
    return [];
  }
}

export function saveOpenSessionTabIds(ids: string[]): void {
  try {
    window.localStorage.setItem(OPEN_SESSION_TABS_KEY, JSON.stringify([...new Set(ids)]));
    window.localStorage.removeItem(LEGACY_CLOSED_SESSION_TABS_KEY);
  } catch {
    // Storage is optional; tabs still behave correctly for the current page load.
  }
}
