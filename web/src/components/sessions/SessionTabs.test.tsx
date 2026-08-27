import { fireEvent, render, screen } from '@/test-utils';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { Plugin, SessionSummary } from '@/protocol.gen';
import { SessionTabs } from './SessionTabs';

const session: SessionSummary = {
  sessionId: 'sess-live',
  title: 'Fix the worker',
  workingDir: '/code/project',
  createdAt: '2025-01-01T00:00:00Z',
  messages: 4,
  live: true,
  chatId: 'chat-live',
  runState: 'running',
};

const plugin: Plugin = {
  apiVersion: 1,
  id: 'system-info',
  name: 'System Info',
  description: 'Inspect the system',
  version: '1.0.0',
  fingerprint: 'abc123',
  entryUrl: '/api/plugins/system-info/assets/abc123/index.js',
  pages: [],
};

describe('SessionTabs', () => {
  it('opens and closes live sessions from tabs', async () => {
    const onOpen = vi.fn();
    const onClose = vi.fn();
    render(
      <SessionTabs
        sessions={[session, { ...session, sessionId: 'other', chatId: 'other-chat', title: 'Other' }]}
        activeSessionId="other"
        busy={false}
        canCreateChat={true}
        onNewChat={vi.fn()}
        onOpen={onOpen}
        onClose={onClose}
        onReorder={vi.fn()}
      />,
    );

    await userEvent.click(screen.getByRole('tab', { name: 'Fix the worker — Running' }));
    expect(onOpen).toHaveBeenCalledWith('sess-live', '/code/project');

    await userEvent.click(screen.getByRole('button', { name: 'Close live session Fix the worker' }));
    expect(onClose).toHaveBeenCalledWith('sess-live', 'chat-live');
    expect(screen.getByRole('tab', { name: 'Other — Running' })).toBeEnabled();
    await userEvent.click(screen.getByRole('tab', { name: 'Other — Running' }));
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it('is always visible and opens an unpersisted new-chat tab from the plus button', async () => {
    const onNewChat = vi.fn();
    const { container } = render(
      <SessionTabs
        sessions={[]}
        activeSessionId={null}
        busy={false}
        canCreateChat={true}
        onNewChat={onNewChat}
        onOpen={vi.fn()}
        onClose={vi.fn()}
        onReorder={vi.fn()}
      />,
    );

    expect(container.querySelector('.session-tabs')).toBeVisible();
    expect(screen.getByRole('tab', { name: 'New chat' })).toHaveAttribute('aria-current', 'page');
    await userEvent.click(screen.getByRole('button', { name: 'Open new chat tab' }));
    expect(onNewChat).toHaveBeenCalledOnce();
  });

  it('disables new chat when no workspace is open', () => {
    render(
      <SessionTabs
        sessions={[]}
        activeSessionId={null}
        busy={false}
        canCreateChat={false}
        onNewChat={vi.fn()}
        onOpen={vi.fn()}
        onClose={vi.fn()}
        onReorder={vi.fn()}
      />,
    );

    expect(screen.getByRole('button', { name: 'Open new chat tab' })).toBeDisabled();
  });

  it('offers vertical and horizontal splits for the active tab', async () => {
    const onSplit = vi.fn();
    render(
      <SessionTabs
        sessions={[session]}
        activeSessionId="sess-live"
        busy={false}
        canCreateChat={true}
        onNewChat={vi.fn()}
        onOpen={vi.fn()}
        onClose={vi.fn()}
        onReorder={vi.fn()}
        onSplit={onSplit}
      />,
    );

    await userEvent.click(screen.getByRole('button', { name: 'Split active tab vertically' }));
    await userEvent.click(screen.getByRole('button', { name: 'Split active tab horizontally' }));

    expect(onSplit).toHaveBeenNthCalledWith(1, 'sess-live', '/code/project', 'vertical');
    expect(onSplit).toHaveBeenNthCalledWith(2, 'sess-live', '/code/project', 'horizontal');
  });

  it('selects and closes plugin tabs', async () => {
    const onOpenPlugin = vi.fn();
    const onClosePlugin = vi.fn();
    render(
      <SessionTabs
        sessions={[session]}
        activeSessionId="sess-live"
        plugins={[{ plugin, path: 'overview' }]}
        activePluginId={null}
        busy={false}
        canCreateChat={true}
        onNewChat={vi.fn()}
        onOpen={vi.fn()}
        onClose={vi.fn()}
        onReorder={vi.fn()}
        onOpenPlugin={onOpenPlugin}
        onClosePlugin={onClosePlugin}
      />,
    );

    await userEvent.click(screen.getByRole('tab', { name: 'System Info plugin' }));
    expect(onOpenPlugin).toHaveBeenCalledWith('system-info', 'overview');

    await userEvent.click(screen.getByRole('button', { name: 'Close plugin System Info' }));
    expect(onClosePlugin).toHaveBeenCalledWith('system-info');
  });

  it('does not reopen an already selected plugin tab', async () => {
    const onOpenPlugin = vi.fn();
    render(
      <SessionTabs
        sessions={[]}
        activeSessionId={null}
        plugins={[{ plugin, path: '' }]}
        activePluginId="system-info"
        busy={false}
        canCreateChat={false}
        onNewChat={vi.fn()}
        onOpen={vi.fn()}
        onClose={vi.fn()}
        onReorder={vi.fn()}
        onOpenPlugin={onOpenPlugin}
      />,
    );

    await userEvent.click(screen.getByRole('tab', { name: 'System Info plugin' }));
    expect(onOpenPlugin).not.toHaveBeenCalled();
  });

  it('reorders tabs with drag and drop', () => {
    const onReorder = vi.fn();
    render(
      <SessionTabs
        sessions={[session, { ...session, sessionId: 'other', title: 'Other' }]}
        activeSessionId="sess-live"
        busy={false}
        canCreateChat={true}
        onNewChat={vi.fn()}
        onOpen={vi.fn()}
        onClose={vi.fn()}
        onReorder={onReorder}
      />,
    );

    const dragged = screen.getByRole('tab', { name: 'Fix the worker — Running' }).parentElement!;
    const target = screen.getByRole('tab', { name: 'Other — Running' }).parentElement!;
    const dataTransfer = { effectAllowed: '', dropEffect: '', setData: vi.fn() };
    fireEvent.dragStart(dragged, { dataTransfer });
    fireEvent.dragOver(target, { dataTransfer });
    fireEvent.drop(target, { dataTransfer });

    expect(onReorder).toHaveBeenCalledWith('sess-live', 'other');
  });
});
