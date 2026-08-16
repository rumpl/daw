import { createRef } from 'react';
import { fireEvent, render, screen, waitFor } from '@/test-utils';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { Bootstrap, Plugin, SessionSummary, Workspace } from '@/protocol.gen';
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
  executionTarget: 'host',
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
  workspaceHints: [],
} as unknown as Bootstrap;

describe('Sidebar', () => {
  it('opens the workspace selector and allows selecting the current workspace', async () => {
    const onOpenWorkspace = vi.fn();
    render(
      <Sidebar
        boot={boot}
        workspace={workspace}
        sessions={[]}
        recentWorkspaces={['/code/other']}
        plugins={[]}
        pluginErrors={[]}
        activePluginId={null}
        activePluginPath=""
        activeSessionId={null}
        workspacePath={workspace.path}
        busy={false}
        drawerRef={createRef<HTMLDivElement>()}
        onWorkspacePathChange={vi.fn()}
        onOpenWorkspace={onOpenWorkspace}
        onNewChat={vi.fn()}
        onResumeChat={vi.fn()}
        onOpenPlugin={vi.fn()}
      />,
    );

    const user = userEvent.setup();
    const switcher = screen.getByRole('button', { name: /current/i });
    // Base UI opens mouse menus on mousedown; exercise the full browser press sequence.
    const pointerDown = new Event('pointerdown', { bubbles: true });
    Object.defineProperties(pointerDown, {
      pointerType: { value: 'mouse' },
      button: { value: 0 },
    });
    fireEvent(switcher, pointerDown);
    fireEvent.mouseDown(switcher, { button: 0 });
    await waitFor(() => expect(switcher).toHaveAttribute('aria-expanded', 'true'));
    fireEvent.mouseUp(switcher, { button: 0 });
    fireEvent.click(switcher, { button: 0 });
    expect(switcher).toHaveAttribute('aria-expanded', 'true');

    const currentWorkspace = screen.getByRole('menuitem', { name: /current/i });
    expect(currentWorkspace).toBeEnabled();
    expect(currentWorkspace).toHaveAttribute('aria-current', 'page');
    await user.click(currentWorkspace);

    expect(onOpenWorkspace).toHaveBeenCalledWith(workspace.path);
    expect(switcher).toHaveAttribute('aria-expanded', 'false');
  });

  it('starts a blank chat without forwarding the click event as a message', async () => {
    const onNewChat = vi.fn();
    render(
      <Sidebar
        boot={boot}
        workspace={workspace}
        sessions={[]}
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
        onNewChat={onNewChat}
        onResumeChat={vi.fn()}
        onOpenPlugin={vi.fn()}
      />,
    );

    await userEvent.click(screen.getByRole('button', { name: 'New chat' }));
    expect(onNewChat).toHaveBeenCalledWith();
  });

  it('opens a global plugin from its contributed sidebar item', async () => {
    const onOpenPlugin = vi.fn();
    render(
      <Sidebar
        boot={boot}
        workspace={workspace}
        sessions={[]}
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
        onOpenPlugin={onOpenPlugin}
      />,
    );

    await userEvent.click(screen.getByRole('button', { name: 'System info' }));
    expect(onOpenPlugin).toHaveBeenCalledWith('system-info', '');
  });

  it('highlights and scrolls the active session into view', async () => {
    const scrollIntoView = vi.fn();
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
      configurable: true,
      value: scrollIntoView,
    });

    render(
      <Sidebar
        boot={boot} workspace={workspace} sessions={[liveSession]} recentWorkspaces={[]} plugins={[]}
        pluginErrors={[]} activePluginId={null} activePluginPath="" activeSessionId={liveSession.sessionId}
        workspacePath={workspace.path} busy={false} drawerRef={createRef<HTMLDivElement>()}
        onWorkspacePathChange={vi.fn()} onOpenWorkspace={vi.fn()} onNewChat={vi.fn()}
        onResumeChat={vi.fn()} onOpenPlugin={vi.fn()}
      />,
    );

    const activeSession = screen.getByRole('button', { name: /Fix the worker/ });
    expect(activeSession).toHaveAttribute('aria-current', 'page');
    expect(activeSession).toHaveAttribute('title', expect.stringContaining('Host'));
    expect(activeSession.querySelector('.lucide-laptop')).toBeInTheDocument();
    await waitFor(() => expect(scrollIntoView).toHaveBeenCalledWith({ block: 'nearest' }));

    delete (HTMLElement.prototype as { scrollIntoView?: unknown }).scrollIntoView;
  });

  it('groups sessions by day', () => {
    const sessions: SessionSummary[] = [
      { ...liveSession, sessionId: 'today', title: 'Today session', createdAt: new Date().toISOString() },
      { ...liveSession, sessionId: 'older', title: 'Older session', createdAt: '2025-01-01T00:00:00Z' },
    ];
    render(
      <Sidebar
        boot={boot}
        workspace={workspace}
        sessions={sessions}
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
        onResumeChat={vi.fn()}
        onOpenPlugin={vi.fn()}
      />,
    );

    expect(screen.getByRole('heading', { name: 'Today' })).toBeVisible();
    expect(screen.getByRole('heading', { name: /Wednesday, Jan 1/ })).toBeVisible();
  });

  it('renders creation provenance as a session tree', () => {
    const sessions: SessionSummary[] = [
      { ...liveSession, sessionId: 'parent', title: 'Parent task', createdAt: new Date().toISOString() },
      { ...liveSession, sessionId: 'child', parentSessionId: 'parent', title: 'Delegated task', createdAt: new Date().toISOString() },
    ];
    render(
      <Sidebar
        boot={boot} workspace={workspace} sessions={sessions} recentWorkspaces={[]} plugins={[]}
        pluginErrors={[]} activePluginId={null} activePluginPath="" activeSessionId={null} workspacePath={workspace.path}
        busy={false} drawerRef={createRef<HTMLDivElement>()} onWorkspacePathChange={vi.fn()}
        onOpenWorkspace={vi.fn()} onNewChat={vi.fn()} onResumeChat={vi.fn()} onOpenPlugin={vi.fn()}
      />,
    );

    const parent = screen.getByRole('treeitem', { name: /Parent task/ });
    expect(parent).toHaveAttribute('aria-expanded', 'true');
    expect(parent.querySelector('[role="group"]')).toContainElement(screen.getByText('Delegated task').closest('[role="treeitem"]'));
  });

  it('shows live state in the sessions list without a separate live-sessions menu', () => {
    render(
      <Sidebar
        boot={boot}
        workspace={workspace}
        sessions={[liveSession, {
          ...liveSession,
          sessionId: 'sess-idle',
          title: 'Idle session',
          runState: 'idle',
        }]}
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
        onResumeChat={vi.fn()}
        onOpenPlugin={vi.fn()}
      />,
    );

    expect(screen.getByText('Fix the worker')).toBeVisible();
    expect(screen.getByText('Idle session')).toBeVisible();
    expect(screen.getAllByLabelText('Running')).toHaveLength(1);
    expect(document.querySelectorAll('.session-title .run-dot')).toHaveLength(1);
    expect(document.querySelector('.session-title .run-dot')).toHaveClass('run-running');
    expect(screen.queryByText(/messages/)).not.toBeInTheDocument();
    expect(screen.queryByText('Running')).not.toBeInTheDocument();
    expect(screen.queryByText(/Not running/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Live sessions/)).not.toBeInTheDocument();
  });
});
