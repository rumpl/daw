import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { SessionSummary } from '../protocol.gen';
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

    await userEvent.click(screen.getByRole('button', { name: 'Fix the worker — Running' }));
    expect(onOpen).toHaveBeenCalledWith('sess-live', '/code/project');

    await userEvent.click(screen.getByRole('button', { name: 'Close live session Fix the worker' }));
    expect(onClose).toHaveBeenCalledWith('sess-live', 'chat-live');
    expect(screen.getByRole('button', { name: 'Other — Running' })).toBeEnabled();
    await userEvent.click(screen.getByRole('button', { name: 'Other — Running' }));
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it('is always visible and starts a new chat from the plus button', async () => {
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
    await userEvent.click(screen.getByRole('button', { name: 'Create new chat' }));
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

    expect(screen.getByRole('button', { name: 'Create new chat' })).toBeDisabled();
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

    const dragged = screen.getByRole('button', { name: 'Fix the worker — Running' }).parentElement!;
    const target = screen.getByRole('button', { name: 'Other — Running' }).parentElement!;
    const dataTransfer = { effectAllowed: '', dropEffect: '', setData: vi.fn() };
    fireEvent.dragStart(dragged, { dataTransfer });
    fireEvent.dragOver(target, { dataTransfer });
    fireEvent.drop(target, { dataTransfer });

    expect(onReorder).toHaveBeenCalledWith('sess-live', 'other');
  });
});
