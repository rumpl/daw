import { Button } from '@/components/ui/button';
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu';
import { Input } from '@/components/ui/input';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { ChevronDown, Folder, FolderOpen, Plus, Search, Settings } from 'lucide-react';
import { useMemo, useState, type RefObject } from 'react';
import type { Bootstrap, Plugin, PluginError, SessionSummary, Workspace } from '@/protocol.gen';
import { clip } from '@/safety';
import { PluginSlotView } from '@/components/plugins/PluginSlotView';
import { groupSessionsByDay } from './sessionTree';
import { SessionTreeItem } from './SessionTreeItem';

interface SidebarProps {
  boot: Bootstrap;
  workspace: Workspace | null;
  sessions: SessionSummary[];
  recentWorkspaces: string[];
  plugins: Plugin[];
  pluginErrors: PluginError[];
  activePluginId: string | null;
  activePluginPath: string;
  activeSessionId?: string | null;
  workspacePath: string;
  busy: boolean;
  drawerRef: RefObject<HTMLDivElement | null>;
  onWorkspacePathChange: (path: string) => void;
  onOpenWorkspace: (path: string) => void;
  onNewChat: () => void;
  onResumeChat: (sessionId: string, workspacePath?: string) => void;
  onOpenPlugin: (pluginId: string, path: string) => void;
  onOpenPluginSettings?: () => void;
  pluginSettingsActive?: boolean;
}

function projectLabel(path: string) {
  return path.split('/').filter(Boolean).slice(-2).join('/') || path;
}

export function Sidebar({
  boot,
  workspace,
  sessions,
  recentWorkspaces,
  plugins,
  pluginErrors,
  activePluginId,
  activePluginPath,
  activeSessionId = null,
  workspacePath,
  busy,
  drawerRef,
  onWorkspacePathChange,
  onOpenWorkspace,
  onNewChat,
  onResumeChat,
  onOpenPlugin,
  onOpenPluginSettings,
  pluginSettingsActive,
}: SidebarProps) {
  const contributionContext = { workspace, chatId: null, session: null };
  const [sessionFilter, setSessionFilter] = useState('');
  const [projectPickerOpen, setProjectPickerOpen] = useState(false);
  const [showPathInput, setShowPathInput] = useState(false);

  const filteredSessions = useMemo(() => {
    const query = sessionFilter.trim().toLowerCase();
    if (!query) return sessions;
    const byID = new Map(sessions.map((session) => [session.sessionId, session]));
    const included = new Set<string>();
    for (const session of sessions) {
      if (!session.title.toLowerCase().includes(query) && !session.sessionId.toLowerCase().includes(query)) continue;
      let current: SessionSummary | undefined = session;
      while (current && !included.has(current.sessionId)) {
        included.add(current.sessionId);
        current = current.parentSessionId ? byID.get(current.parentSessionId) : undefined;
      }
    }
    return sessions.filter((session) => included.has(session.sessionId));
  }, [sessions, sessionFilter]);

  const groupedSessions = useMemo(() => groupSessionsByDay(filteredSessions), [filteredSessions]);

  const projectWorkspaces = useMemo(
    () => Array.from(new Set([workspace?.path, ...recentWorkspaces].filter((path): path is string => Boolean(path)))),
    [recentWorkspaces, workspace?.path],
  );
  const openProject = (path: string) => {
    setProjectPickerOpen(false);
    setShowPathInput(false);
    onOpenWorkspace(path);
  };

  return (
    <div className="sidebar-inner" ref={drawerRef} aria-busy={busy || undefined}>
      <div className="brand">
        docker-agent<span className="brand-sub"> dashboard</span>
      </div>

      <DropdownMenu open={projectPickerOpen} onOpenChange={setProjectPickerOpen}>
        <DropdownMenuTrigger render={
          <Button type="button" variant="outline" className="project-switcher">
            <Folder aria-hidden="true" />
            <span className="project-label">
              <span className="project-name">{workspace ? clip(projectLabel(workspace.path), 60) : 'Choose a project'}</span>
              <span className="project-path">{workspace ? clip(workspace.path, 120) : 'Select a working directory'}</span>
            </span>
            <ChevronDown className="project-chevron" aria-hidden="true" />
          </Button>
        } />
        <DropdownMenuContent align="start" sideOffset={6} className="project-menu">
          <DropdownMenuGroup>
          <DropdownMenuLabel>Recent projects</DropdownMenuLabel>
          {projectWorkspaces.map((path) => {
            const current = path === workspace?.path;
            return (
              <DropdownMenuItem key={path} onClick={() => openProject(path)} disabled={busy}
                aria-current={current ? 'page' : undefined}>
                <Folder aria-hidden="true" />
                <div className="project-menu-label flex flex-col">
                  <span className="font-medium">{clip(projectLabel(path), 60)}</span>
                  <span className="truncate text-xs text-muted-foreground">{clip(path, 160)}</span>
                </div>
              </DropdownMenuItem>
            );
          })}
          {projectWorkspaces.length === 0 ? <DropdownMenuItem disabled>No recent projects</DropdownMenuItem> : null}
          </DropdownMenuGroup>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={() => setShowPathInput(true)}>
            <FolderOpen aria-hidden="true" /> Open another directory…
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <Dialog open={showPathInput} onOpenChange={setShowPathInput}>
        <DialogContent className="sm:max-w-md" aria-describedby="open-project-description">
          <DialogTitle>Open a project</DialogTitle>
          <DialogDescription id="open-project-description">Enter the absolute path to a working directory.</DialogDescription>
          <form onSubmit={(event) => { event.preventDefault(); openProject(workspacePath); }} className="flex flex-col gap-3 pt-2">
            <Input aria-label="Working directory path" value={workspacePath}
              onChange={(event) => onWorkspacePathChange(event.target.value)}
              placeholder="/absolute/path/to/project" list="ws-hints" autoFocus />
            <datalist id="ws-hints">{recentWorkspaces.map((path) => <option key={path} value={path} />)}</datalist>
            <div className="flex justify-end gap-3">
              <DialogClose render={<Button type="button" variant="secondary" />}>Cancel</DialogClose>
              <Button type="submit" disabled={busy || !workspacePath.trim()}>Open project</Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>

      <Button
        type="button"
        className="new-chat-button w-full"
        onClick={() => onNewChat()}
        disabled={!workspace || busy}
      >
        <Plus size={15} aria-hidden="true" /> New chat
      </Button>

      {plugins.some((plugin) => plugin.pages?.some((page) => page.sidebar)) ? (
        <nav className="plugin-navigation" aria-label="Plugins">
          <p className="sidebar-heading">Plugins</p>
          <ul>
            {plugins.flatMap((plugin) =>
              (plugin.pages ?? []).filter((page) => page.sidebar).map((page) => (
                <li key={`${plugin.id}:${page.id}`}>
                  <Button type="button" variant="ghost"
                    aria-current={activePluginId === plugin.id && activePluginPath === page.path ? 'page' : undefined}
                    title={plugin.description || plugin.name} onClick={() => onOpenPlugin(plugin.id, page.path)}>
                    {clip(page.label, 60)}
                  </Button>
                </li>
              )),
            )}
          </ul>
        </nav>
      ) : null}
      {pluginErrors.length > 0 ? (
        <p className="plugin-discovery-error" title={pluginErrors.map((error) => `${error.pluginId}: ${error.message}`).join('\n')}>
          {pluginErrors.length} invalid plugin{pluginErrors.length === 1 ? '' : 's'}
        </p>
      ) : null}

      <div className="sidebar-section-heading flex items-center justify-between">
        <span className="text-sm font-medium">Sessions</span>
        <span className="text-xs text-muted-foreground">{sessions.length}</span>
      </div>

      <section className="sidebar-panel" aria-label="Sessions">
          <label className="sr-only" htmlFor="session-search">Search sessions</label>
          <div className="relative">
            <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
            <Input id="session-search" value={sessionFilter} className="pl-8"
              onChange={(event) => setSessionFilter(event.target.value)} placeholder="Search sessions" />
          </div>
          {filteredSessions.length === 0 ? (
            <p className="hint">
              {workspace ? 'No sessions yet for this project.' : 'Choose a project to see sessions.'}
            </p>
          ) : (
            <ScrollArea className="session-list">
              {groupedSessions.map((group, index) => (
                <section
                  className={`session-day${group.label === 'Today' ? ' session-day-current' : ''}`}
                  key={group.label}
                  aria-labelledby={`session-day-${index}`}
                >
                  <h3 id={`session-day-${index}`}>{group.label}</h3>
                  <ul role="tree">
                    {group.sessions.map((node) => (
                      <SessionTreeItem key={node.session.sessionId} node={node} busy={busy}
                        activeSessionId={activeSessionId} onResumeChat={onResumeChat} />
                    ))}
                  </ul>
                </section>
              ))}
            </ScrollArea>
          )}
      </section>

      <PluginSlotView slot="sidebar.footer" context={contributionContext} />
      <footer className="sidebar-footer">
        <Tooltip>
          <TooltipTrigger render={
            <Button type="button" size="icon-sm" variant="ghost" className="sidebar-settings-button"
              aria-label="Settings" aria-current={pluginSettingsActive ? 'page' : undefined}
              onClick={() => onOpenPluginSettings?.()}>
              <Settings aria-hidden="true" />
            </Button>
          } />
          <TooltipContent>Settings</TooltipContent>
        </Tooltip>
        <span className="sidebar-version text-xs text-muted-foreground">docker-agent {clip(boot.agentVersion, 40)}</span>
      </footer>
    </div>
  );
}
