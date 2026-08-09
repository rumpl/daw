import { useEffect, useState, type RefObject } from 'react';
import { ChatHeader } from './ChatHeader';
import { PluginActionButtons, PluginSlotView } from './PluginSurfaces';
import { Composer } from './Composer';
import { Conversation } from './Conversation';
import { NewChatPrompt } from './NewChatPrompt';
import { PendingDialogs } from './PendingDialogs';
import { clip } from '../safety';
import { usePluginContributions } from '../plugin-contributions';
import type { useDashboard } from '../useDashboard';

type DashboardController = ReturnType<typeof useDashboard>;

interface ChatPaneProps {
  dashboard: DashboardController;
  menuButton: RefObject<HTMLButtonElement | null>;
  showMenu?: boolean;
}

export function ChatPane({ dashboard, menuButton, showMenu = true }: ChatPaneProps) {
  const [newChatMessage, setNewChatMessage] = useState('');
  const { commands: pluginCommands } = usePluginContributions();
  const contributionContext = {
    workspace: dashboard.workspace,
    chatId: dashboard.chatId,
    session: dashboard.state.meta,
  };

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
    if (dashboard.chatId) dashboard.send(resolved, mode);
    else dashboard.newChat(resolved);
  };

  useEffect(() => {
    if (dashboard.chatId) setNewChatMessage('');
  }, [dashboard.chatId]);

  return (
    <>
      <ChatHeader
        hasChat={Boolean(dashboard.chatId)}
        state={dashboard.state}
        models={dashboard.models}
        busyAction={dashboard.busyAction}
        menuButton={menuButton}
        drawerOpen={dashboard.drawerOpen}
        onToggleDrawer={showMenu ? () => dashboard.setDrawerOpen((open) => !open) : () => undefined}
        onPatchConfig={dashboard.patchConfig}
        onCompact={dashboard.compact}
        onRename={dashboard.rename}
        showMenu={showMenu}
      />

      {dashboard.error ? <p className="banner banner-error" role="alert">{clip(dashboard.error, 300)}</p> : null}
      {dashboard.connection === 'disconnected' && dashboard.chatId ? (
        <p className="banner banner-warn">
          Disconnected from the event stream.
          <button type="button" onClick={() => void dashboard.resnapshot()}>Retry now</button>
        </p>
      ) : null}
      {dashboard.state.closed ? (
        <p className="banner banner-warn">
          This chat was closed ({clip(dashboard.state.closedReason, 80)}).
        </p>
      ) : null}

      <Conversation
        items={dashboard.state.items}
        queue={dashboard.state.run.queue}
        empty={
          !dashboard.workspace ? (
            <><h2>Pick a working directory</h2><p>Open a folder in the sidebar and start a chat.</p></>
          ) : dashboard.state.items.length === 0 ? (
            <NewChatPrompt
              workspaceLabel={dashboard.workspace.label}
              workspacePath={dashboard.workspace.path}
              message={newChatMessage}
              busy={dashboard.busyAction}
              onMessageChange={setNewChatMessage}
              onSubmit={(message) => {
                setNewChatMessage('');
                void sendWithPlugins(message, 'normal');
              }}
            />
          ) : (
            <><h2>Say something</h2><p>Ask for a change, a review, or an explanation. Tools run in {clip(dashboard.workspace.label, 40)}.</p></>
          )
        }
      />

      {dashboard.chatId && dashboard.state.items.length > 0 ? (
        <>
          <PluginActionButtons location="composer" context={contributionContext} />
          <PluginSlotView slot="composer.actions" context={contributionContext} />
          <Composer
          draft={dashboard.draft}
          onDraftChange={dashboard.setDraft}
          run={dashboard.state.run}
          disabled={dashboard.busyAction || dashboard.state.closed}
          commands={[...dashboard.commands, ...pluginCommands.map((command) => ({name: command.name, description: command.description, kind: 'plugin'}))]}
          attachments={dashboard.attachments}
          uploading={dashboard.uploading}
          onAddAttachments={dashboard.addAttachments}
          onRemoveAttachment={dashboard.removeAttachment}
          onSend={(text, mode) => void sendWithPlugins(text, mode)}
          onStop={dashboard.abort}
          />
        </>
      ) : null}

      <PendingDialogs
        state={dashboard.state}
        onToolDecision={dashboard.decideTool}
        onElicitationAnswer={dashboard.answerElicitation}
      />
    </>
  );
}
