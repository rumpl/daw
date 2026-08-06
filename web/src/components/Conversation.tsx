import { memo, useEffect, useRef, useState } from 'react';
import type { Item, MessageItem, Notice, ToolActivity, Transfer, Summary } from '../protocol.gen';
import { Markdown } from '../Markdown';
import { clip } from '../safety';
import { itemKey } from '../reducer';

const BOTTOM_THRESHOLD_PX = 96;

function MessageBubble({ message }: { message: MessageItem }) {
  const isUser = message.role === 'user';
  const who = isUser ? 'You' : clip(message.agentName || 'assistant', 60);
  return (
    <article className={`msg msg-${isUser ? 'user' : 'assistant'}`} aria-label={`${message.role} message`}>
      <header className="msg-head">
        <span className="msg-role">{who}</span>
        {message.model ? <span className="msg-model">{clip(message.model, 60)}</span> : null}
      </header>

      {/* Reasoning is model output: always visible, escaped text, never Markdown. */}
      {message.reasoning ? <p className="reasoning">{message.reasoning}</p> : null}

      {/*
        Streaming text is escaped plain text; only the completed message is
        rendered as GFM Markdown. This keeps partial, unbalanced Markdown from
        being interpreted mid-stream.
      */}
      {message.streaming ? (
        <pre className="msg-streaming" aria-live="polite">
          {message.text}
          <span className="caret" aria-hidden="true" />
        </pre>
      ) : isUser ? (
        <pre className="msg-plain">{message.text}</pre>
      ) : (
        <Markdown>{message.text}</Markdown>
      )}
    </article>
  );
}

function ToolCard({ tool }: { tool: ToolActivity }) {
  const state = tool.state;
  // Only failures and in-flight calls get any colour; success is silent.
  const tone = state === 'error' ? 'error' : state === 'rejected' ? 'rejected' : state === 'success' ? 'ok' : 'running';
  return (
    <details className={`tool tool-${tone}`} aria-label={`tool ${tool.name}`}>
      <summary>
        <span className="tool-name">{clip(tool.name, 60)}</span>
        <span className="tool-args">{clip(tool.argsSummary, 160)}</span>
        <span className="tool-state">{state === 'awaiting_confirmation' ? 'waiting' : state}</span>
      </summary>
      {tool.preview ? (
        <pre className="tool-output" tabIndex={0}>
          {tool.preview}
        </pre>
      ) : null}
      {tool.truncated ? <p className="tool-note">Output truncated for display.</p> : null}
    </details>
  );
}

function TransferCard({ transfer }: { transfer: Transfer }) {
  return (
    <p className="transfer" aria-label="sub-agent transfer">
      <span>
        {clip(transfer.fromAgent, 60) || 'agent'} → <strong>{clip(transfer.toAgent, 60)}</strong>
      </span>
    </p>
  );
}

function NoticeCard({ notice }: { notice: Notice }) {
  return (
    <p className={`notice notice-${notice.level}`} role={notice.level === 'error' ? 'alert' : undefined}>
      {clip(notice.message, 600)}
    </p>
  );
}

function SummaryCard({ summary }: { summary: Summary }) {
  return (
    <details className="summary">
      <summary>Compacted history</summary>
      <pre className="summary-body">{summary.text}</pre>
    </details>
  );
}

const Row = memo(function Row({ item }: { item: Item }) {
  switch (item.kind) {
    case 'message':
      return item.message ? <MessageBubble message={item.message} /> : null;
    case 'tool':
      return item.tool ? <ToolCard tool={item.tool} /> : null;
    case 'transfer':
      return item.transfer ? <TransferCard transfer={item.transfer} /> : null;
    case 'notice':
      return item.notice ? <NoticeCard notice={item.notice} /> : null;
    case 'summary':
      return item.summary ? <SummaryCard summary={item.summary} /> : null;
    default:
      return null;
  }
});

export function Conversation({ items, empty }: { items: Item[]; empty: React.ReactNode }) {
  const ref = useRef<HTMLDivElement | null>(null);
  const [pinned, setPinned] = useState(true);

  useEffect(() => {
    const el = ref.current;
    if (!el || !pinned) return;
    el.scrollTop = el.scrollHeight;
  }, [items, pinned]);

  const onScroll = () => {
    const el = ref.current;
    if (!el) return;
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
    setPinned(distance <= BOTTOM_THRESHOLD_PX);
  };

  return (
    <div className="conversation-wrap">
      <div className="conversation" ref={ref} onScroll={onScroll} role="log" aria-live="polite" aria-label="Conversation">
        {items.length === 0 ? <div className="empty">{empty}</div> : null}
        {items.map((item) => (
          <Row key={itemKey(item)} item={item} />
        ))}
      </div>
      {!pinned ? (
        <button
          type="button"
          className="jump"
          onClick={() => {
            setPinned(true);
            const el = ref.current;
            if (el) el.scrollTop = el.scrollHeight;
          }}
        >
          Jump to latest
        </button>
      ) : null}
    </div>
  );
}
