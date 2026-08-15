import { Button } from '@/components/ui/button';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { PanelBottom, PanelRight, Plus, X } from 'lucide-react';
import { useState, type DragEvent, type ReactElement } from 'react';
import type { Plugin, SessionSummary } from '@/protocol.gen';
import { clip } from '@/safety';
import { cn } from '@/lib/utils';
import { usePluginContributions } from '@/plugin-contributions';
import { PluginSlotView } from '@/components/plugins/PluginSlotView';

interface PluginTab {
  plugin: Plugin;
  path: string;
}

interface SessionTabsProps {
  sessions: SessionSummary[];
  activeSessionId: string | null;
  plugins?: PluginTab[];
  activePluginId?: string | null;
  busy: boolean;
  canCreateChat: boolean;
  onNewChat: () => void;
  onOpen: (sessionId: string, workspacePath: string) => void;
  onClose: (sessionId: string, chatId: string) => void;
  onReorder: (draggedSessionId: string, targetSessionId: string) => void;
  onOpenPlugin?: (pluginId: string, path: string) => void;
  onClosePlugin?: (pluginId: string) => void;
  onSplit?: (sessionId: string, workspacePath: string, direction: 'vertical' | 'horizontal') => void;
}

export function SessionTabs({
  sessions,
  activeSessionId,
  plugins = [],
  activePluginId = null,
  busy,
  canCreateChat,
  onNewChat,
  onOpen,
  onClose,
  onReorder,
  onOpenPlugin,
  onClosePlugin,
  onSplit,
}: SessionTabsProps) {
  const [draggedSessionId, setDraggedSessionId] = useState<string | null>(null);
  const [dropTargetId, setDropTargetId] = useState<string | null>(null);
  const { badges } = usePluginContributions();

  const finishDrag = () => {
    setDraggedSessionId(null);
    setDropTargetId(null);
  };

  const dropTab = (event: DragEvent<HTMLDivElement>, targetSessionId: string) => {
    event.preventDefault();
    if (draggedSessionId && draggedSessionId !== targetSessionId) onReorder(draggedSessionId, targetSessionId);
    finishDrag();
  };

  const activeValue = activePluginId ? `plugin:${activePluginId}` : activeSessionId ? `session:${activeSessionId}` : '';
  const iconTooltip = (label: string, button: ReactElement) => (
    <Tooltip>
      <TooltipTrigger render={button} />
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );

  return (
    <Tabs value={activeValue} className="session-tabs-root block min-w-0 gap-0 bg-muted/40">
      <TabsList className="session-tabs h-10 w-full justify-start gap-1 overflow-x-auto rounded-none border-b bg-muted/40 px-5 py-1" aria-label="Open tabs">
        {sessions.map((session) => {
          const title = session.title || 'Untitled';
          const active = session.sessionId === activeSessionId;
          const runStatus = session.runState === 'running' ? 'Running' : session.runState === 'stopping' ? 'Stopping' : 'Not running';
          return (
            <div
              className={cn(
                'session-tab group/tab relative flex h-8 min-w-20 max-w-50 flex-1 items-center overflow-hidden rounded-md hover:bg-background/50',
                active && 'bg-background shadow-sm ring-1 ring-border',
                draggedSessionId === session.sessionId && 'opacity-50',
                dropTargetId === session.sessionId && 'ring-2 ring-primary',
              )}
              data-active={active || undefined}
              data-dragging={draggedSessionId === session.sessionId || undefined}
              data-drop-target={dropTargetId === session.sessionId || undefined}
              draggable={!busy}
              key={session.sessionId}
              onDragStart={(event) => {
                setDraggedSessionId(session.sessionId);
                event.dataTransfer.effectAllowed = 'move';
                event.dataTransfer.setData('text/plain', session.sessionId);
              }}
              onDragOver={(event) => {
                if (!draggedSessionId || draggedSessionId === session.sessionId) return;
                event.preventDefault();
                event.dataTransfer.dropEffect = 'move';
                setDropTargetId(session.sessionId);
              }}
              onDragLeave={() => setDropTargetId((current) => current === session.sessionId ? null : current)}
              onDrop={(event) => dropTab(event, session.sessionId)}
              onDragEnd={finishDrag}
            >
              <TabsTrigger
                value={`session:${session.sessionId}`}
                className="session-tab-open h-full min-w-0 flex-1 justify-start overflow-hidden bg-transparent pr-7 pl-2 shadow-none data-active:bg-transparent [&>span:first-child]:truncate"
                data-running={session.runState === 'running' || undefined}
                aria-current={active ? 'page' : undefined}
                title={`${title} — ${runStatus} — ${session.workingDir}`}
                aria-label={`${title} — ${runStatus}`}
                onClick={() => { if (!active) onOpen(session.sessionId, session.workingDir); }}
                disabled={busy}
              >
                <span>{clip(title, 50)}</span>
                {session.runState === 'running' ? <span className="session-tab-status size-1.5 shrink-0 rounded-full bg-emerald-500" aria-hidden="true" /> : null}
                {badges.filter((badge) => badge.sessionId === session.sessionId).map((badge) => (
                  <span className={`plugin-session-badge plugin-session-badge-${badge.tone}`} key={badge._key}>{badge.value}</span>
                ))}
                <PluginSlotView slot="session-tab.badge" context={{workspace: null, chatId: session.chatId ?? null, session: null, sessionId: session.sessionId}} />
              </TabsTrigger>
              {iconTooltip('Close session',
                <Button type="button" size="icon-xs" variant="ghost" className="session-tab-close absolute right-1 z-10"
                  aria-label={`Close live session ${title}`} onClick={() => onClose(session.sessionId, session.chatId ?? '')}
                  disabled={busy || !session.chatId}>
                  <X size={13} aria-hidden="true" />
                </Button>,
              )}
            </div>
          );
        })}

        {plugins.map(({ plugin, path }) => {
          const active = plugin.id === activePluginId;
          return (
            <div className={cn(
              'session-tab plugin-tab group/tab relative flex h-8 min-w-20 max-w-50 flex-1 items-center overflow-hidden rounded-md hover:bg-background/50',
              active && 'bg-background shadow-sm ring-1 ring-border',
            )} data-active={active || undefined} key={`plugin:${plugin.id}`}>
              <TabsTrigger value={`plugin:${plugin.id}`} className="session-tab-open h-full min-w-0 flex-1 justify-start overflow-hidden bg-transparent pr-7 pl-2 shadow-none data-active:bg-transparent [&>span:first-child]:truncate"
                aria-current={active ? 'page' : undefined} title={plugin.description || plugin.name}
                aria-label={`${plugin.name} plugin`} onClick={() => { if (!active) onOpenPlugin?.(plugin.id, path); }}>
                <span>{clip(plugin.name, 50)}</span>
              </TabsTrigger>
              {iconTooltip('Close plugin',
                <Button type="button" size="icon-xs" variant="ghost" className="session-tab-close absolute right-1 z-10"
                  aria-label={`Close plugin ${plugin.name}`} onClick={() => onClosePlugin?.(plugin.id)}>
                  <X size={13} aria-hidden="true" />
                </Button>,
              )}
            </div>
          );
        })}

        {iconTooltip('New chat',
          <Button type="button" size="icon-sm" variant="ghost" className="session-tab-new sticky right-1 z-20 bg-muted"
            aria-label="Create new chat" onClick={onNewChat} disabled={busy || !canCreateChat}>
            <Plus size={15} aria-hidden="true" />
          </Button>,
        )}

        {onSplit && activeSessionId ? (
          <div className="session-split-actions ml-auto flex items-center gap-1 pl-1" aria-label="Split editor">
            <Button type="button" size="icon-xs" variant="ghost" aria-label="Split active tab vertically"
              onClick={() => {
                const active = sessions.find((item) => item.sessionId === activeSessionId);
                if (active) onSplit(active.sessionId, active.workingDir, 'vertical');
              }} disabled={busy}>
              <PanelRight size={14} aria-hidden="true" />
            </Button>
            <Button type="button" size="icon-xs" variant="ghost" aria-label="Split active tab horizontally"
              onClick={() => {
                const active = sessions.find((item) => item.sessionId === activeSessionId);
                if (active) onSplit(active.sessionId, active.workingDir, 'horizontal');
              }} disabled={busy}>
              <PanelBottom size={14} aria-hidden="true" />
            </Button>
          </div>
        ) : null}
      </TabsList>
    </Tabs>
  );
}
