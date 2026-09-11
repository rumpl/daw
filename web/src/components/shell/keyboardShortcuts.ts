export type SplitDirection = 'vertical' | 'horizontal';

export function newTabSplitShortcut(event: Pick<KeyboardEvent, 'key' | 'metaKey' | 'shiftKey' | 'altKey' | 'ctrlKey'>): SplitDirection | null {
  if (!event.metaKey || event.altKey || event.ctrlKey || event.key.toLowerCase() !== 'd') return null;
  return event.shiftKey ? 'horizontal' : 'vertical';
}
