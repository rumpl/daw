export interface SplitPaneState {
  id: string;
  sessionId: string;
  workspacePath: string;
}

export type PaneLayout =
  | { type: 'leaf'; id: string }
  | { type: 'split'; id: string; direction: 'vertical' | 'horizontal'; size: number; first: PaneLayout; second: PaneLayout };

export const PRIMARY_PANE_ID = 'primary';

export function splitLeaf(layout: PaneLayout, paneId: string, pane: SplitPaneState, direction: 'vertical' | 'horizontal'): PaneLayout {
  if (layout.type === 'leaf') {
    return layout.id === paneId
      ? { type: 'split', id: `group-${pane.id}`, direction, size: 50, first: layout, second: { type: 'leaf', id: pane.id } }
      : layout;
  }
  return { ...layout, first: splitLeaf(layout.first, paneId, pane, direction), second: splitLeaf(layout.second, paneId, pane, direction) };
}

export function removeLeaf(layout: PaneLayout, paneId: string): PaneLayout {
  if (layout.type === 'leaf') return layout;
  if (layout.first.type === 'leaf' && layout.first.id === paneId) return layout.second;
  if (layout.second.type === 'leaf' && layout.second.id === paneId) return layout.first;
  return { ...layout, first: removeLeaf(layout.first, paneId), second: removeLeaf(layout.second, paneId) };
}

export function updateSplitSize(layout: PaneLayout, splitId: string, size: number): PaneLayout {
  if (layout.type === 'leaf') return layout;
  if (layout.id === splitId) return { ...layout, size };
  return { ...layout, first: updateSplitSize(layout.first, splitId, size), second: updateSplitSize(layout.second, splitId, size) };
}
