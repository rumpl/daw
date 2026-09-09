import { Button } from '@/components/ui/button';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible';
import { Input } from '@/components/ui/input';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { ChevronRight, Folder, FolderPlus, PanelLeftClose, Plus, Search, Settings, Trash2 } from 'lucide-react';
import { useMemo, useState, type DragEvent, type RefObject } from 'react';
import type { Bootstrap, Plugin, PluginError, ProjectFolder, SessionSummary, Workspace } from '@/protocol.gen';
import { clip } from '@/safety';
import { PluginSlotView } from '@/components/plugins/PluginSlotView';
import { groupSessionsByDay } from './sessionTree';
import { SessionTreeItem } from './SessionTreeItem';

interface SidebarProps {
  boot: Bootstrap;
  workspace: Workspace | null;
  sessions: SessionSummary[];
  allSessions?: SessionSummary[];
  recentWorkspaces: string[];
  projectFolders?: ProjectFolder[];
  onProjectFoldersChange?: (folders: ProjectFolder[]) => void;
  plugins: Plugin[];
  pluginErrors: PluginError[];
  activePluginId: string | null;
  activePluginPath: string;
  activeSessionId?: string | null;
  workspacePath?: string;
  busy: boolean;
  drawerRef: RefObject<HTMLDivElement | null>;
  onWorkspacePathChange?: (path: string) => void;
  onOpenWorkspace?: (path: string) => void;
  onRemoveWorkspace?: (path: string) => void;
  onNewChat: (workspacePath: string) => void;
  onResumeChat: (sessionId: string, workspacePath?: string) => void;
  onStarSession?: (sessionId: string, starred: boolean) => void;
  onOpenPlugin: (pluginId: string, path: string) => void;
  onOpenSettings?: () => void;
  onOpenProjectSettings?: () => void;
  onCollapse?: () => void;
  settingsActive?: boolean;
}

function projectLabel(path: string) {
  return path.split('/').filter(Boolean).slice(-2).join('/') || path;
}

export function Sidebar({
  workspace,
  sessions,
  allSessions = sessions,
  recentWorkspaces,
  projectFolders = [],
  onProjectFoldersChange = () => undefined,
  plugins,
  pluginErrors,
  activePluginId,
  activePluginPath,
  activeSessionId = null,
  busy,
  drawerRef,
  onNewChat,
  onResumeChat,
  onStarSession = () => undefined,
  onOpenPlugin,
  onOpenSettings,
  onOpenProjectSettings,
  onCollapse,
  settingsActive,
}: SidebarProps) {
  const contributionContext = { workspace, chatId: null, session: null };
  const [sessionFilter, setSessionFilter] = useState('');
  const [creatingFolder, setCreatingFolder] = useState(false);
  const [folderName, setFolderName] = useState('');

  const projects = useMemo(() => {
    const paths = Array.from(new Set(recentWorkspaces))
      .sort((left, right) => projectLabel(left).localeCompare(projectLabel(right), undefined, { sensitivity: 'base' }));
    const query = sessionFilter.trim().toLowerCase();
    return paths.map((path) => {
      const matchingSessions = allSessions.filter((session) => session.workingDir === path);
      const projectSessions = path === workspace?.path
        ? Array.from(new Map([...matchingSessions, ...sessions].map((session) => [session.sessionId, session])).values())
        : matchingSessions;
      const filtered = !query ? projectSessions : projectSessions.filter((session) =>
        session.title.toLowerCase().includes(query) || session.sessionId.toLowerCase().includes(query));
      return {
        path,
        sessions: filtered,
        running: projectSessions.filter((session) => session.runState === 'running').length,
      };
    }).filter((project) => !query || projectLabel(project.path).toLowerCase().includes(query) || project.sessions.length > 0);
  }, [allSessions, recentWorkspaces, sessionFilter, sessions, workspace?.path]);
  const projectsByPath = useMemo(() => new Map(projects.map((project) => [project.path, project])), [projects]);
  const groupedPaths = new Set(projectFolders.flatMap((folder) => folder.paths ?? []));
  const ungroupedProjects = projects.filter((project) => !groupedPaths.has(project.path));

  const createFolder = () => {
    const name = folderName.trim();
    if (!name) return;
    onProjectFoldersChange([...projectFolders, { id: crypto.randomUUID(), name, paths: [] }]);
    setFolderName('');
    setCreatingFolder(false);
  };

  const moveProject = (path: string, folderId: string | null) => {
    onProjectFoldersChange(projectFolders.map((folder) => ({
      ...folder,
      paths: [...(folder.paths ?? []).filter((candidate) => candidate !== path), ...(folder.id === folderId ? [path] : [])],
    })));
  };

  const droppedProject = (event: DragEvent, folderId: string | null) => {
    event.preventDefault();
    const path = event.dataTransfer.getData('application/x-atelier-project');
    if (projectsByPath.has(path)) moveProject(path, folderId);
  };

  const renderProject = (project: (typeof projects)[number]) => {
    const projectNodes = groupSessionsByDay(project.sessions).flatMap((group) => group.sessions);
    return (
      <Collapsible key={project.path} defaultOpen={project.running > 0} className="project-group"
        onDragStart={(event) => {
          event.dataTransfer.effectAllowed = 'move';
          event.dataTransfer.setData('application/x-atelier-project', project.path);
        }}>
        <div className="project-row" draggable>
          <CollapsibleTrigger render={
            <Button type="button" variant="ghost" className="project-open"
              title={project.path} aria-label={`Toggle sessions for ${projectLabel(project.path)}`}>
              <ChevronRight className="project-chevron" aria-hidden="true" />
              <Folder aria-hidden="true" />
              <span>{clip(projectLabel(project.path), 60)}</span>
            </Button>
          } />
          {project.running > 0 ? (
            <span className="project-running" aria-label="Running" title={`${project.running} running`}>
              <span className="run-dot run-running" aria-hidden="true" />
            </span>
          ) : null}
          <Tooltip>
            <TooltipTrigger render={
              <Button type="button" size="icon-xs" variant="ghost" className="project-new-chat"
                aria-label={`New chat in ${projectLabel(project.path)}`}
                onClick={() => onNewChat(project.path)} disabled={busy}>
                <Plus aria-hidden="true" />
              </Button>
            } />
            <TooltipContent>New chat</TooltipContent>
          </Tooltip>
        </div>
        <CollapsibleContent>
          {project.sessions.length === 0 ? <p className="project-empty">No sessions</p> : (
            <ul role="tree" className="project-sessions">
              {projectNodes.map((node) => (
                <SessionTreeItem key={node.session.sessionId} node={node} busy={busy}
                  activeSessionId={activeSessionId}
                  onStarSession={onStarSession}
                  onResumeChat={(sessionId) => onResumeChat(sessionId, project.path)} />
              ))}
            </ul>
          )}
        </CollapsibleContent>
      </Collapsible>
    );
  };


  return (
    <div className="sidebar-inner" ref={drawerRef} aria-busy={busy || undefined}>
      <div className="brand-row">
        <div className="brand">
          Atelier<span className="brand-sub"> coding studio</span>
        </div>
        <Tooltip>
          <TooltipTrigger render={
            <Button type="button" size="icon-xs" variant="ghost" className="sidebar-collapse-button"
              aria-label="Collapse sidebar" onClick={onCollapse}>
              <PanelLeftClose aria-hidden="true" />
            </Button>
          } />
          <TooltipContent>Collapse sidebar</TooltipContent>
        </Tooltip>
      </div>

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

      <div className="sidebar-section-heading">
        <span className="text-sm font-medium">Projects</span>
      </div>

      <section className="sidebar-panel" aria-label="Projects and sessions">
        <label className="sr-only" htmlFor="session-search">Search projects and sessions</label>
        <div className="relative">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
          <Input id="session-search" value={sessionFilter} className="pl-8"
            onChange={(event) => setSessionFilter(event.target.value)} placeholder="Search projects and sessions" />
        </div>
        {projects.length === 0 ? (
          <div className="sidebar-empty-projects">
            <p className="hint">No projects added yet.</p>
            <Button type="button" variant="outline" onClick={() => onOpenProjectSettings?.()}>
              Add project
            </Button>
          </div>
        ) : (
          <div className="project-list">
            {projectFolders.map((folder) => {
              const folderProjects = (folder.paths ?? []).flatMap((path) => {
                const project = projectsByPath.get(path);
                return project ? [project] : [];
              });
              if (sessionFilter && folderProjects.length === 0) return null;
              return (
                <Collapsible key={folder.id} defaultOpen className="project-folder"
                  onDragOver={(event) => event.preventDefault()} onDrop={(event) => droppedProject(event, folder.id)}>
                  <div className="project-folder-row">
                    <CollapsibleTrigger render={
                      <Button type="button" variant="ghost" className="project-folder-open"
                        aria-label={`Toggle folder ${folder.name}`}>
                        <ChevronRight className="project-chevron" aria-hidden="true" />
                        <Folder aria-hidden="true" />
                        <span>{clip(folder.name, 60)}</span>
                      </Button>
                    } />
                    <Button type="button" size="icon-xs" variant="ghost" className="project-folder-delete"
                      aria-label={`Delete folder ${folder.name}`}
                      onClick={() => onProjectFoldersChange(projectFolders.filter((candidate) => candidate.id !== folder.id))}>
                      <Trash2 aria-hidden="true" />
                    </Button>
                  </div>
                  <CollapsibleContent className="project-folder-content">
                    {folderProjects.length === 0 ? <p className="project-folder-empty">Drop projects here</p> : folderProjects.map(renderProject)}
                  </CollapsibleContent>
                </Collapsible>
              );
            })}
            <div className="ungrouped-projects" aria-label="Ungrouped projects"
              onDragOver={(event) => event.preventDefault()} onDrop={(event) => droppedProject(event, null)}>
              {projectFolders.length > 0 && ungroupedProjects.length > 0 ? <p className="project-folder-label">Ungrouped</p> : null}
              {ungroupedProjects.map(renderProject)}
            </div>
            <div className="project-folder-add">
              {creatingFolder ? (
                <form onSubmit={(event) => { event.preventDefault(); createFolder(); }}>
                  <FolderPlus aria-hidden="true" />
                  <Input autoFocus aria-label="Folder name" value={folderName} maxLength={80}
                    onChange={(event) => setFolderName(event.target.value)} placeholder="New folder"
                    onBlur={() => { setCreatingFolder(false); setFolderName(''); }}
                    onKeyDown={(event) => {
                      if (event.key === 'Escape') {
                        event.preventDefault();
                        setCreatingFolder(false);
                        setFolderName('');
                      }
                    }} />
                </form>
              ) : (
                <Button type="button" variant="ghost" className="project-folder-add-button"
                  aria-label="New project folder" onClick={() => setCreatingFolder(true)}>
                  <FolderPlus aria-hidden="true" />
                  <span>New folder</span>
                </Button>
              )}
            </div>
          </div>
        )}
      </section>

      <PluginSlotView slot="sidebar.footer" context={contributionContext} />
      <footer className="sidebar-footer">
        <Tooltip>
          <TooltipTrigger render={
            <Button type="button" size="icon-sm" variant="ghost" className="sidebar-settings-button"
              aria-label="Settings" aria-current={settingsActive ? 'page' : undefined}
              onClick={() => onOpenSettings?.()}>
              <Settings aria-hidden="true" />
            </Button>
          } />
          <TooltipContent>Settings</TooltipContent>
        </Tooltip>
      </footer>
    </div>
  );
}
