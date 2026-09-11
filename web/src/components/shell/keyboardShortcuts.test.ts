import { describe, expect, it } from 'vitest';
import { newTabSplitShortcut } from './keyboardShortcuts';

function shortcut(overrides: Partial<KeyboardEvent> = {}) {
  return {
    key: 'd',
    metaKey: true,
    shiftKey: false,
    altKey: false,
    ctrlKey: false,
    ...overrides,
  } as KeyboardEvent;
}

describe('newTabSplitShortcut', () => {
  it('maps Cmd+D to a vertical split', () => {
    expect(newTabSplitShortcut(shortcut())).toBe('vertical');
  });

  it('maps Cmd+Shift+D to a horizontal split', () => {
    expect(newTabSplitShortcut(shortcut({ shiftKey: true }))).toBe('horizontal');
  });

  it('ignores unrelated or modified shortcuts', () => {
    expect(newTabSplitShortcut(shortcut({ metaKey: false }))).toBeNull();
    expect(newTabSplitShortcut(shortcut({ key: 'k' }))).toBeNull();
    expect(newTabSplitShortcut(shortcut({ altKey: true }))).toBeNull();
    expect(newTabSplitShortcut(shortcut({ ctrlKey: true }))).toBeNull();
  });
});
