import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { useMemo, type RefObject } from 'react';
import { ChatHeader } from './ChatHeader';
import { PluginActionButtons } from '@/components/plugins/PluginActionButtons';
import { PluginSlotView } from '@/components/plugins/PluginSlotView';
import { SessionSideView } from '@/components/plugins/SessionSideView';
import { Composer } from './Composer';
import { Conversation } from '@/components/conversation/Conversation';
import { PendingDialogs } from '@/components/dialogs/PendingDialogs';
import { clip } from '@/safety';
import { usePluginContributions, type ContributionContext, type PluginCommand } from '@/plugin-contributions';
import { useDraft } from '@/hooks/useDraft';
import type { useDashboard } from '@/hooks/useDashboard';

type DashboardController = ReturnType<typeof useDashboard>;

interface ChatPaneProps {
  dashboard: DashboardController;
  menuButton: RefObject<HTMLButtonElement | null>;
  showMenu?: boolean;
}

export function ChatPane({ dashboard, menuButton, showMenu = true }: ChatPaneProps) {
  const { commands: pluginCommands } = usePluginContributions();
  const contributionContext = useMemo(() => ({
    workspace: dashboard.workspace,
    chatId: dashboard.chatId,
    session: dashboard.state.meta,
    sessionId: dashboard.state.meta?.sessionId ?? dashboard.activeSessionId ?? undefined,
  }), [dashboard.activeSessionId, dashboard.chatId, dashboard.state.meta, dashboard.workspace]);

  return (
    <div className="chat-pane-layout">
      <div className="chat-pane-primary">
      <ChatHeader
        hasChat={Boolean(dashboard.chatId)}
        state={dashboard.state}
        busyAction={dashboard.busyAction}
        menuButton={menuButton}
        drawerOpen={dashboard.drawerOpen}
        onToggleDrawer={showMenu ? () => dashboard.setDrawerOpen((open) => !open) : () => undefined}
        onRename={dashboard.rename}
        showMenu={showMenu}
      />

      {dashboard.error ? <Alert className="banner banner-error rounded-none border-x-0 border-t-0" variant="destructive"><AlertDescription>{clip(dashboard.error, 300)}</AlertDescription></Alert> : null}
      {dashboard.connection === 'disconnected' && dashboard.chatId ? (
        <Alert className="banner banner-warn rounded-none border-x-0 border-t-0">
          <AlertDescription className="flex items-center gap-3">
            Disconnected from the event stream.
            <Button type="button" size="sm" variant="secondary" onClick={() => void dashboard.resnapshot()}>Retry now</Button>
          </AlertDescription>
        </Alert>
      ) : null}
      {dashboard.state.closed ? (
        <Alert className="banner banner-warn rounded-none border-x-0 border-t-0">
          <AlertDescription>This chat was closed ({clip(dashboard.state.closedReason, 80)}).</AlertDescription>
        </Alert>
      ) : null}

      <Conversation
        items={dashboard.state.items}
        queue={dashboard.state.run.queue}
        contributionContext={contributionContext}
        empty={
          !dashboard.workspace ? (
            <><h2>Pick a working directory</h2><p>Open a folder in the sidebar and start a chat.</p></>
          ) : dashboard.chatId ? (
            <><h2>Say something</h2><p>Ask for a change, a review, or an explanation. Tools run in {clip(dashboard.workspace.label, 40)}.</p></>
          ) : (
            <><h2>Start a chat</h2><p>Send a message below to begin working in {clip(dashboard.workspace.label, 40)}.</p></>
          )
        }
      />

      {dashboard.chatId ? (
        <>
          <PluginActionButtons location="composer" context={contributionContext} />
          <PluginSlotView slot="composer.actions" context={contributionContext} />
        </>
      ) : null}
      <ChatComposer dashboard={dashboard} pluginCommands={pluginCommands} contributionContext={contributionContext} />

      <PendingDialogs
        state={dashboard.state}
        onToolDecision={dashboard.decideTool}
        onElicitationAnswer={dashboard.answerElicitation}
      />
      </div>
      <SessionSideView context={contributionContext} />
    </div>
  );
}

function ChatComposer({ dashboard, pluginCommands, contributionContext }: {
  dashboard: DashboardController;
  pluginCommands: PluginCommand[];
  contributionContext: ContributionContext;
}) {
  const { draft, setDraft } = useDraft(dashboard.activeSessionId);

  const sendWithPlugins = async (text: string, mode: 'normal' | 'steer' | 'followUp') => {
    let resolved = text;
    if (mode === 'normal' && text.startsWith('/')) {
      const [name, ...rest] = text.slice(1).split(/\s+/);
      const command = pluginCommands.find((item) => item.name === name);
      if (command) {
        const result = await command.run(rest.join(' '), contributionContext);
        if (result === undefined) return;
        resolved = result;
      }
    }
    const sent = dashboard.chatId
      ? await dashboard.send(resolved, mode)
      : await dashboard.newChat(resolved);
    if (sent) setDraft('');
  };

  return <Composer
    draft={draft}
    onDraftChange={setDraft}
    run={dashboard.state.run}
    disabled={!dashboard.workspace || dashboard.busyAction || dashboard.state.closed}
    placeholder={dashboard.workspace ? undefined : 'Choose a project to start a chat…'}
    focusKey={dashboard.activeSessionId}
    commands={[...dashboard.commands, ...pluginCommands.map((command) => ({ name: command.name, description: command.description, kind: 'plugin' }))]}
    attachments={dashboard.attachments}
    uploading={dashboard.uploading}
    models={dashboard.models}
    currentModel={dashboard.state.meta?.model ?? dashboard.defaultModel}
    thinkingLevel={dashboard.state.meta?.thinkingLevel ?? dashboard.defaultThinkingLevel}
    thinkingLevels={dashboard.state.meta?.thinkingLevels ?? dashboard.defaultThinkingLevels}
    tools={dashboard.tools}
    configDisabled={dashboard.busyAction || dashboard.state.run.state !== 'idle'}
    toolsDisabled={dashboard.busyAction}
    compactDisabled={!dashboard.chatId || dashboard.busyAction || dashboard.state.run.state !== 'idle'}
    usageTokens={dashboard.state.usage.inputTokens + dashboard.state.usage.outputTokens}
    usageCost={dashboard.state.usage.cost}
    onSelectModel={(model) => dashboard.patchConfig({ model })}
    onSelectThinking={(thinkingLevel) => dashboard.patchConfig({ thinkingLevel })}
    onToolChange={dashboard.setToolEnabled}
    onRefreshTools={dashboard.refreshChatOptions}
    onCompact={dashboard.compact}
    onAddAttachments={dashboard.addAttachments}
    onRemoveAttachment={dashboard.removeAttachment}
    onSend={(text, mode) => void sendWithPlugins(text, mode)}
    onStop={dashboard.abort}
  />;
}
