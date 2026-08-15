import { useEffect, useState } from 'react';
import { api } from '@/api';
import type { CommandInfo } from '@/protocol.gen';
import { clip } from '@/safety';
import { useChat } from '@/hooks/useChat';
import { useDraft } from '@/hooks/useDraft';
import { Composer, type SendMode } from '@/components/chat/Composer';
import { Conversation } from '@/components/conversation/Conversation';
import { PendingDialogs } from '@/components/dialogs/PendingDialogs';

// PluginChat is a stable, high-level host component for plugins that want to
// embed an existing dashboard chat without rebuilding streaming, composer,
// command completion, confirmation, and elicitation behavior.
export function PluginChat({ chatId }: { chatId: string }) {
  const { state, connection } = useChat(chatId || null);
  const { draft, setDraft } = useDraft(state.meta?.sessionId ?? null);
  const [commands, setCommands] = useState<CommandInfo[]>([]);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!chatId) {
      setCommands([]);
      return;
    }
    let active = true;
    void api.commands(chatId).then((next) => {
      if (active) setCommands(next);
    }).catch((cause: unknown) => {
      if (active) setError(cause instanceof Error ? cause.message : 'commands could not be loaded');
    });
    return () => {
      active = false;
    };
  }, [chatId]);

  const run = async (action: () => Promise<unknown>) => {
    setError('');
    try {
      await action();
    } catch (cause: unknown) {
      setError(cause instanceof Error ? cause.message : 'the chat request failed');
    }
  };

  const send = (text: string, mode: SendMode) => {
    void run(async () => {
      await api.send(chatId, text, mode);
      setDraft('');
    });
  };

  return (
    <section className="plugin-chat" aria-label="Embedded chat">
      <div className="plugin-chat-status">
        <span className={`conn conn-${connection}`}>
          <span className="conn-dot" aria-hidden="true" />
          <span>{connection}</span>
        </span>
      </div>
      {error ? <p className="banner banner-error" role="alert">{clip(error, 300)}</p> : null}
      <Conversation
        items={state.items}
        queue={state.run.queue}
        contributionContext={{workspace: null, chatId, session: state.meta, sessionId: state.meta?.sessionId}}
        empty={<><h2>Say something</h2><p>This chat has no messages yet.</p></>}
      />
      <Composer
        draft={draft}
        onDraftChange={setDraft}
        run={state.run}
        disabled={!chatId || state.closed}
        commands={commands}
        attachments={[]}
        uploading={false}
        onAddAttachments={() => undefined}
        onRemoveAttachment={() => undefined}
        onSend={send}
        onStop={() => void run(() => api.abort(chatId))}
      />
      <PendingDialogs
        state={state}
        onToolDecision={(decision, reason) => void run(async () => {
          const request = state.confirmations[0];
          if (request) await api.confirmTool(chatId, { toolCallId: request.toolCallId, decision, reason });
        })}
        onElicitationAnswer={(action, content) => void run(async () => {
          const request = state.elicitations[0];
          if (request) await api.answerElicitation(chatId, { elicitationId: request.elicitationId, action, content });
        })}
      />
    </section>
  );
}
