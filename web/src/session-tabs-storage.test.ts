import { beforeEach, describe, expect, it } from 'vitest';
import { loadOpenSessionTabIds, saveOpenSessionTabIds } from './session-tabs-storage';

const values = new Map<string, string>();
const storage: Storage = {
  get length() { return values.size; },
  clear: () => values.clear(),
  getItem: (key) => values.get(key) ?? null,
  key: (index) => [...values.keys()][index] ?? null,
  removeItem: (key) => { values.delete(key); },
  setItem: (key, value) => { values.set(key, value); },
};
Object.defineProperty(window, 'localStorage', { configurable: true, value: storage });

describe('open session tab storage', () => {
  beforeEach(() => window.localStorage.clear());

  it('round trips ordered open session IDs', () => {
    saveOpenSessionTabIds(['session-two', 'session-one']);

    expect(loadOpenSessionTabIds()).toEqual(['session-two', 'session-one']);
  });

  it('treats absent and invalid storage as no open tabs', () => {
    expect(loadOpenSessionTabIds()).toEqual([]);

    window.localStorage.setItem('dawui.open-session-tabs.v1', '{not json');
    expect(loadOpenSessionTabIds()).toEqual([]);

    window.localStorage.setItem('dawui.open-session-tabs.v1', JSON.stringify(['valid', 42, null, '', 'valid']));
    expect(loadOpenSessionTabIds()).toEqual(['valid']);
  });

  it('persists an explicitly empty tab list and removes the legacy closed list', () => {
    window.localStorage.setItem('dawui.closed-session-tabs.v1', JSON.stringify(['old']));
    saveOpenSessionTabIds([]);

    expect(window.localStorage.getItem('dawui.open-session-tabs.v1')).toBe('[]');
    expect(window.localStorage.getItem('dawui.closed-session-tabs.v1')).toBeNull();
  });
});
