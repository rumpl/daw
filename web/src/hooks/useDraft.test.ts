import { beforeEach, describe, expect, it } from 'vitest';
import { clearDraft, migrateDraft } from './useDraft';

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

describe('draft storage', () => {
  beforeEach(() => window.localStorage.clear());

  it('removes the persisted draft for a sent message', () => {
    window.localStorage.setItem('dawui.draft.session-one', 'Send me');

    clearDraft('session-one');

    expect(window.localStorage.getItem('dawui.draft.session-one')).toBeNull();
  });

  it('can clear a draft after a client-side session is migrated', () => {
    window.localStorage.setItem('dawui.draft.draft:one', 'Send me');

    migrateDraft('draft:one', 'session-one');
    clearDraft('session-one');

    expect(window.localStorage.getItem('dawui.draft.draft:one')).toBeNull();
    expect(window.localStorage.getItem('dawui.draft.session-one')).toBeNull();
  });
});
