import { createRef } from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { Bootstrap, Plugin, SessionSummary, Workspace } from '../protocol.gen';
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

const plugin: Plugin = {
  apiVersion: 1,
  id: 'system-info',
  name: 'System info',
  description: 'Shows system information',
  version: '1.0.0',
  fingerprint: 'abc123',
  entryUrl: '/api/plugins/system-info/assets/abc123/index.js',
  pages: [{ id: 'overview', path: '', label: 'System info', sidebar: true }],
};

const boot = {
  agentVersion: 'test',
  workspaceRoots: ['/code'],
  workspaceHints: [],
} as unknown as Bootstrap;

describe('Sidebar', () => {
  it('opens a global plugin from its contributed sidebar item', async () => {
    const onOpenPlugin = vi.fn();
    render(
      <Sidebar
        boot={boot}
        workspace={workspace}
        sessions={[]}
        liveSessions={[]}
        recentWorkspaces={[]}
        plugins={[plugin]}
        pluginErrors={[]}
        activePluginId={null}
        activePluginPath=""
        workspacePath={workspace.path}
        busy={false}
        drawerRef={createRef<HTMLDivElement>()}
        onWorkspacePathChange={vi.fn()}
        onOpenWorkspace={vi.fn()}
        onNewChat={vi.fn()}
        onResumeChat={vi.fn()}
        onCloseLiveSession={vi.fn()}
        onOpenPlugin={onOpenPlugin}
      />,
    );

    await userEvent.click(screen.getByRole('button', { name: 'System info' }));
    expect(onOpenPlugin).toHaveBeenCalledWith('system-info', '');
  });

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
        plugins={[]}
        pluginErrors={[]}
        activePluginId={null}
        activePluginPath=""
        workspacePath={workspace.path}
        busy={false}
        drawerRef={createRef<HTMLDivElement>()}
        onWorkspacePathChange={vi.fn()}
        onOpenWorkspace={vi.fn()}
        onNewChat={vi.fn()}
        onResumeChat={onResumeChat}
        onCloseLiveSession={onCloseLiveSession}
        onOpenPlugin={vi.fn()}
      />,
    );

    const liveTab = screen.getByRole('tab', { name: /Live sessions 1/i });
    expect(liveTab).toHaveAttribute('aria-selected', 'false');
    await userEvent.click(liveTab);
    expect(screen.getByText('Running')).toBeVisible();

    await userEvent.click(
      screen.getByRole('button', { name: 'Open live session Fix the worker in /code/other-project' }),
    );
    expect(onResumeChat).toHaveBeenCalledWith('sess-live', '/code/other-project');

    await userEvent.click(screen.getByText('•••'));
    await userEvent.click(screen.getByRole('button', { name: 'Close live session Fix the worker' }));
    expect(onCloseLiveSession).toHaveBeenCalledWith('sess-live', 'chat-live');
  });
});
