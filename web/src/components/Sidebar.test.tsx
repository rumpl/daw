import { createRef } from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { Bootstrap, SessionSummary, Workspace } from '../protocol.gen';
import { Sidebar } from './Sidebar';

const workspace: Workspace = {
  workspaceId: 'ws-current',
  path: '/code/current',
  label: 'current',
  agentsMd: false,
  agentsIgnore: false,
  notices: [],
};

const liveSession: SessionSummary = {
  sessionId: 'sess-live',
  title: 'Fix the worker',
  workingDir: '/code/other-project',
  createdAt: '2025-01-01T00:00:00Z',
  messages: 4,
  live: true,
  chatId: 'chat-live',
  runState: 'running',
};

const boot = {
  agentVersion: 'test',
  workspaceRoots: ['/code'],
  workspaceHints: [],
} as unknown as Bootstrap;

describe('Sidebar', () => {
  it('opens a live session from another project directly', async () => {
    const onResumeChat = vi.fn();
    const onCloseLiveSession = vi.fn();
    render(
      <Sidebar
        boot={boot}
        workspace={workspace}
        sessions={[]}
        liveSessions={[liveSession]}
        recentWorkspaces={[]}
        workspacePath={workspace.path}
        busy={false}
        drawerRef={createRef<HTMLDivElement>()}
        onWorkspacePathChange={vi.fn()}
        onOpenWorkspace={vi.fn()}
        onNewChat={vi.fn()}
        onResumeChat={onResumeChat}
        onCloseLiveSession={onCloseLiveSession}
      />,
    );

    expect(screen.getByRole('heading', { name: /Live sessions 1/i })).toBeVisible();
    expect(screen.getByText('Running')).toBeVisible();

    await userEvent.click(
      screen.getByRole('button', { name: 'Open live session Fix the worker in /code/other-project' }),
    );
    expect(onResumeChat).toHaveBeenCalledWith('sess-live', '/code/other-project');

    await userEvent.click(screen.getByRole('button', { name: 'Close live session Fix the worker' }));
    expect(onCloseLiveSession).toHaveBeenCalledWith('sess-live', 'chat-live');
  });
});
