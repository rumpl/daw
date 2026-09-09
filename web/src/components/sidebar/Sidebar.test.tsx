import { createRef } from 'react';
import { render, screen, waitFor } from '@/test-utils';
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
  starred: false,
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
  it('creates folders and moves projects into them', async () => {
    const onProjectFoldersChange = vi.fn();
    const { rerender } = render(
      <Sidebar
        boot={boot} workspace={workspace} sessions={[]} recentWorkspaces={[workspace.path, '/code/other']} plugins={[]}
        pluginErrors={[]} activePluginId={null} activePluginPath="" busy={false}
        drawerRef={createRef<HTMLDivElement>()} onNewChat={vi.fn()} onResumeChat={vi.fn()}
        onOpenPlugin={vi.fn()} projectFolders={[]} onProjectFoldersChange={onProjectFoldersChange}
      />,
    );

    await userEvent.click(screen.getByRole('button', { name: 'New project folder' }));
    await userEvent.type(screen.getByRole('textbox', { name: 'Folder name' }), 'Work{Enter}');
    expect(onProjectFoldersChange).toHaveBeenCalledWith([
      expect.objectContaining({ name: 'Work', paths: [] }),
    ]);

    onProjectFoldersChange.mockClear();
    rerender(
      <Sidebar
        boot={boot} workspace={workspace} sessions={[]} recentWorkspaces={[workspace.path, '/code/other']} plugins={[]}
        pluginErrors={[]} activePluginId={null} activePluginPath="" busy={false}
        drawerRef={createRef<HTMLDivElement>()} onNewChat={vi.fn()} onResumeChat={vi.fn()}
        onOpenPlugin={vi.fn()} projectFolders={[{ id: 'work', name: 'Work', paths: [] }]}
        onProjectFoldersChange={onProjectFoldersChange}
      />,
    );
    const project = screen.getByRole('button', { name: 'Toggle sessions for code/current' }).closest('[draggable="true"]');
    const folder = screen.getByRole('button', { name: 'Toggle folder Work' }).closest('.project-folder');
    expect(project).not.toBeNull();
    expect(folder).not.toBeNull();
    const transfer = { getData: vi.fn(() => workspace.path) };
    const drop = new Event('drop', { bubbles: true, cancelable: true });
    Object.defineProperty(drop, 'dataTransfer', { value: transfer });
    folder!.dispatchEvent(drop);
    expect(onProjectFoldersChange).toHaveBeenCalledWith([{ id: 'work', name: 'Work', paths: [workspace.path] }]);
  });

  it('dismisses an unfinished folder name when focus leaves the input', async () => {
    const onProjectFoldersChange = vi.fn();
    render(
      <Sidebar
        boot={boot} workspace={workspace} sessions={[]} recentWorkspaces={[workspace.path]} plugins={[]}
        pluginErrors={[]} activePluginId={null} activePluginPath="" busy={false}
        drawerRef={createRef<HTMLDivElement>()} onNewChat={vi.fn()} onResumeChat={vi.fn()}
        onOpenPlugin={vi.fn()} onProjectFoldersChange={onProjectFoldersChange}
      />,
    );

    await userEvent.click(screen.getByRole('button', { name: 'New project folder' }));
    await userEvent.type(screen.getByRole('textbox', { name: 'Folder name' }), 'Never saved');
    await userEvent.tab();
    expect(screen.queryByRole('textbox', { name: 'Folder name' })).not.toBeInTheDocument();
    expect(onProjectFoldersChange).not.toHaveBeenCalled();
  });

  it('offers an accessible control to collapse the sidebar', async () => {
    const onCollapse = vi.fn();
    render(
      <Sidebar
        boot={boot} workspace={workspace} sessions={[]} recentWorkspaces={[workspace.path]} plugins={[]}
        pluginErrors={[]} activePluginId={null} activePluginPath="" busy={false}
        drawerRef={createRef<HTMLDivElement>()} onNewChat={vi.fn()} onResumeChat={vi.fn()}
        onOpenPlugin={vi.fn()} onCollapse={onCollapse}
      />,
    );

    await userEvent.click(screen.getByRole('button', { name: 'Collapse sidebar' }));
    expect(onCollapse).toHaveBeenCalledOnce();
  });

  it('lists projects without marking one active', () => {
    render(
      <Sidebar
        boot={boot}
        workspace={workspace}
        sessions={[]}
        recentWorkspaces={[workspace.path, '/code/other']}
        plugins={[]}
        pluginErrors={[]}
        activePluginId={null}
        activePluginPath=""
        activeSessionId={null}
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

    expect(screen.getByRole('button', { name: 'Toggle sessions for code/current' })).not.toHaveAttribute('aria-current');
    expect(screen.getByRole('button', { name: 'Toggle sessions for code/other' })).toBeVisible();
  });

  it('keeps projects sorted alphabetically', () => {
    render(
      <Sidebar
        boot={boot} workspace={{ ...workspace, path: '/code/zulu' }} sessions={[]}
        recentWorkspaces={['/code/zulu', '/code/middle', '/code/alpha']} plugins={[]} pluginErrors={[]}
        activePluginId={null} activePluginPath="" activeSessionId={null} workspacePath="/code/zulu"
        busy={false} drawerRef={createRef<HTMLDivElement>()} onWorkspacePathChange={vi.fn()}
        onOpenWorkspace={vi.fn()} onNewChat={vi.fn()} onResumeChat={vi.fn()} onOpenPlugin={vi.fn()}
      />,
    );

    const projectNames = screen.getAllByRole('button', { name: /Toggle sessions for/ })
      .map((button) => button.getAttribute('aria-label'));
    expect(projectNames).toEqual([
      'Toggle sessions for code/alpha',
      'Toggle sessions for code/middle',
      'Toggle sessions for code/zulu',
    ]);
  });

  it('only lists projects added to DAW, not every historical session directory', () => {
    render(
      <Sidebar
        boot={boot} workspace={workspace} sessions={[]} recentWorkspaces={[workspace.path, '/code/other']}
        allSessions={[liveSession, { ...liveSession, sessionId: 'old', workingDir: '/code/not-added' }]}
        plugins={[]} pluginErrors={[]} activePluginId={null} activePluginPath="" activeSessionId={null}
        workspacePath={workspace.path} busy={false} drawerRef={createRef<HTMLDivElement>()}
        onWorkspacePathChange={vi.fn()} onOpenWorkspace={vi.fn()} onNewChat={vi.fn()}
        onResumeChat={vi.fn()} onOpenPlugin={vi.fn()}
      />,
    );

    expect(screen.getByRole('button', { name: 'Toggle sessions for code/current' })).toBeVisible();
    expect(screen.getByRole('button', { name: 'Toggle sessions for code/other' })).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Toggle sessions for code/not-added' })).not.toBeInTheDocument();
  });

  it('starts a blank chat in a specific project', async () => {
    const onNewChat = vi.fn();
    render(
      <Sidebar
        boot={boot}
        workspace={workspace}
        sessions={[]}
        recentWorkspaces={[workspace.path]}
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

    await userEvent.click(screen.getByRole('button', { name: 'New chat in code/current' }));
    expect(onNewChat).toHaveBeenCalledWith(workspace.path);
  });

  it('opens a global plugin from its contributed sidebar item', async () => {
    const onOpenPlugin = vi.fn();
    render(
      <Sidebar
        boot={boot}
        workspace={workspace}
        sessions={[]}
        recentWorkspaces={[workspace.path]}
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
        boot={boot} workspace={workspace} sessions={[liveSession]} recentWorkspaces={[workspace.path]} plugins={[]}
        pluginErrors={[]} activePluginId={null} activePluginPath="" activeSessionId={liveSession.sessionId}
        workspacePath={workspace.path} busy={false} drawerRef={createRef<HTMLDivElement>()}
        onWorkspacePathChange={vi.fn()} onOpenWorkspace={vi.fn()} onNewChat={vi.fn()}
        onResumeChat={vi.fn()} onOpenPlugin={vi.fn()}
      />,
    );

    const activeSession = screen.getByRole('button', { name: /Fix the worker/ });
    expect(activeSession).toHaveAttribute('aria-current', 'page');
    expect(activeSession).not.toHaveAttribute('title');
    expect(activeSession.querySelector('.lucide-laptop')).toBeInTheDocument();
    await waitFor(() => expect(scrollIntoView).toHaveBeenCalledWith({ block: 'nearest' }));

    delete (HTMLElement.prototype as { scrollIntoView?: unknown }).scrollIntoView;
  });

  it('renders sessions as a compact list without date headings', () => {
    const sessions: SessionSummary[] = [
      { ...liveSession, sessionId: 'today', title: 'Today session', createdAt: new Date().toISOString() },
      { ...liveSession, sessionId: 'older', title: 'Older session', createdAt: '2025-01-01T00:00:00Z' },
    ];
    render(
      <Sidebar
        boot={boot}
        workspace={workspace}
        sessions={sessions}
        recentWorkspaces={[workspace.path]}
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

    expect(screen.getByText('Today session')).toBeVisible();
    expect(screen.getByText('Older session')).toBeVisible();
    expect(screen.queryByRole('heading', { name: 'Today' })).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: /Wednesday, Jan 1/ })).not.toBeInTheDocument();
  });

  it('stars sessions and pins starred sessions to the top of their project', async () => {
    const onStarSession = vi.fn();
    const sessions: SessionSummary[] = [
      { ...liveSession, sessionId: 'newer', title: 'Newer session', createdAt: '2026-01-02T00:00:00Z' },
      { ...liveSession, sessionId: 'starred', title: 'Starred session', createdAt: '2026-01-01T00:00:00Z', starred: true },
    ];
    render(
      <Sidebar
        boot={boot} workspace={workspace} sessions={sessions} recentWorkspaces={[workspace.path]} plugins={[]}
        pluginErrors={[]} activePluginId={null} activePluginPath="" activeSessionId={null} workspacePath={workspace.path}
        busy={false} drawerRef={createRef<HTMLDivElement>()} onWorkspacePathChange={vi.fn()}
        onOpenWorkspace={vi.fn()} onNewChat={vi.fn()} onResumeChat={vi.fn()} onStarSession={onStarSession}
        onOpenPlugin={vi.fn()}
      />,
    );

    const rows = screen.getAllByRole('treeitem');
    expect(rows[0]).toHaveTextContent('Starred session');
    expect(rows[1]).toHaveTextContent('Newer session');
    await userEvent.click(screen.getByRole('button', { name: 'Unstar session' }));
    expect(onStarSession).toHaveBeenCalledWith('starred', false);
  });

  it('renders session title triggers for styled hover tooltips', () => {
    const longTitle = 'Investigate and repair the complete authentication workflow across every service';
    render(
      <Sidebar
        boot={boot} workspace={workspace} sessions={[{ ...liveSession, title: longTitle }]}
        recentWorkspaces={[workspace.path]} plugins={[]} pluginErrors={[]} activePluginId={null} activePluginPath=""
        activeSessionId={null} workspacePath={workspace.path} busy={false}
        drawerRef={createRef<HTMLDivElement>()} onWorkspacePathChange={vi.fn()} onOpenWorkspace={vi.fn()}
        onNewChat={vi.fn()} onResumeChat={vi.fn()} onOpenPlugin={vi.fn()}
      />,
    );

    const sessionButton = screen.getByRole('button', { name: new RegExp(longTitle) });
    expect(sessionButton).toHaveAttribute('data-base-ui-tooltip-trigger');
    expect(sessionButton).not.toHaveAttribute('title');
  });

  it('renders creation provenance as a session tree', () => {
    const sessions: SessionSummary[] = [
      { ...liveSession, sessionId: 'parent', title: 'Parent task', createdAt: new Date().toISOString() },
      { ...liveSession, sessionId: 'child', parentSessionId: 'parent', title: 'Delegated task', createdAt: new Date().toISOString() },
    ];
    render(
      <Sidebar
        boot={boot} workspace={workspace} sessions={sessions} recentWorkspaces={[workspace.path]} plugins={[]}
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
        recentWorkspaces={[workspace.path]}
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
    expect(screen.getAllByLabelText('Running')).toHaveLength(2);
    expect(document.querySelectorAll('.session-title .run-dot')).toHaveLength(1);
    expect(document.querySelector('.session-title .run-dot')).toHaveClass('run-running');
    expect(screen.queryByText(/messages/)).not.toBeInTheDocument();
    expect(screen.queryByText('Running')).not.toBeInTheDocument();
    expect(screen.queryByText(/Not running/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Live sessions/)).not.toBeInTheDocument();
  });
});
