import { render, screen } from '@testing-library/react';
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
        onOpen={onOpen}
        onClose={onClose}
      />,
    );

    await userEvent.click(screen.getByRole('button', { name: 'Fix the worker — Running' }));
    expect(onOpen).toHaveBeenCalledWith('sess-live', '/code/project');

    await userEvent.click(screen.getByRole('button', { name: 'Close live session Fix the worker' }));
    expect(onClose).toHaveBeenCalledWith('sess-live', 'chat-live');
    expect(screen.getByRole('button', { name: 'Other — Running' })).toBeDisabled();
  });
});
