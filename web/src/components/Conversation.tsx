import { Component, memo, useEffect, useRef, useState } from 'react';
import { Download } from 'lucide-react';
import type { Item, MessageItem, Notice, QueueStatus, Transfer, Summary } from '../protocol.gen';
import { Markdown } from '../Markdown';
import { clip } from '../safety';
import { itemKey } from '../reducer';
import { ToolCard } from './ToolActivity';
import { usePluginContributions } from '../plugin-contributions';

const BOTTOM_THRESHOLD_PX = 96;

function attachmentImageSrc(attachment: NonNullable<MessageItem['attachments']>[number]): string | null {
  if (!attachment.mimeType.startsWith('image/') || !attachment.data) return null;
  return `data:${attachment.mimeType};base64,${attachment.data}`;
}

function markdownFilename(message: MessageItem): string {
  const agent = message.agentName.trim().replace(/[^a-z0-9_-]+/gi, '-').replace(/^-+|-+$/g, '') || 'agent';
  const timestamp = message.createdAt ? message.createdAt.replace(/[:.]/g, '-') : message.id;
  return `${agent}-${timestamp}.md`;
}

function downloadMarkdown(message: MessageItem) {
  const blob = new Blob([message.text], { type: 'text/markdown;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = markdownFilename(message);
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

class PluginRendererBoundary extends Component<{children: React.ReactNode}, {failed: boolean}> {
  override state = {failed: false};
  static getDerivedStateFromError() { return {failed: true}; }
  override render() { return this.state.failed ? <p className="plugin-contribution-error">Plugin renderer failed</p> : this.props.children; }
}

function PluginToolContent({renderer, tool}: {renderer: ReturnType<typeof usePluginContributions>['toolRenderers'][number]; tool: NonNullable<Item['tool']>}) {
  return <>{renderer.render(tool)}</>;
}
function PluginAttachmentContent({renderer, attachment}: {renderer: ReturnType<typeof usePluginContributions>['attachmentRenderers'][number]; attachment: NonNullable<MessageItem['attachments']>[number]}) {
  return <>{renderer.render(attachment)}</>;
}

function MessageBubble({ message, attachmentRenderers }: { message: MessageItem; attachmentRenderers: ReturnType<typeof usePluginContributions>['attachmentRenderers'] }) {
  const isUser = message.role === 'user';
  const canDownload = !isUser && !message.streaming;
  return (
    <article className={`msg msg-${isUser ? 'user' : 'assistant'}${canDownload ? ' msg-downloadable' : ''}`} aria-label={`${message.role} message`}>
      {canDownload ? (
        <button
          type="button"
          className="msg-download"
          aria-label="Download message as Markdown"
          title="Download as Markdown"
          onClick={() => downloadMarkdown(message)}
        >
          <Download size={15} aria-hidden="true" />
        </button>
      ) : null}
      {message.attachments?.length ? (
        <div className="message-attachments" aria-label="Message attachments">
          {message.attachments.map((attachment) => {
            const renderer = attachmentRenderers.find((candidate) => candidate.match(attachment));
            if (renderer) return <PluginRendererBoundary key={attachment.id}><div className="plugin-attachment-renderer"><PluginAttachmentContent renderer={renderer} attachment={attachment} /></div></PluginRendererBoundary>;
            const src = attachmentImageSrc(attachment);
            return src ? (
              <figure className="message-attachment-image" key={attachment.id}>
                <img src={src} alt={attachment.name} />
                <figcaption>{clip(attachment.name, 80)}</figcaption>
              </figure>
            ) : (
              <div className="message-attachment-file" key={attachment.id}>
                <span>{clip(attachment.name, 80)}</span>
                <small>{attachment.mimeType}</small>
              </div>
            );
          })}
        </div>
      ) : null}
      {message.reasoning ? (
        <div className="reasoning">
          <Markdown>{message.reasoning}</Markdown>
        </div>
      ) : null}

      {message.streaming ? (
        <div className="msg-streaming" aria-live="polite">
          <Markdown>{message.text}</Markdown>
          <span className="caret" aria-hidden="true" />
        </div>
      ) : isUser ? (
        <pre className="msg-plain">{message.text}</pre>
      ) : (
        <Markdown>{message.text}</Markdown>
      )}
    </article>
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

const Row = memo(function Row({ item, toolRenderers, attachmentRenderers }: {
  item: Item;
  toolRenderers: ReturnType<typeof usePluginContributions>['toolRenderers'];
  attachmentRenderers: ReturnType<typeof usePluginContributions>['attachmentRenderers'];
}) {
  switch (item.kind) {
    case 'message':
      return item.message ? <MessageBubble message={item.message} attachmentRenderers={attachmentRenderers} /> : null;
    case 'tool':
      if (!item.tool) return null;
      const renderer = toolRenderers.find((candidate) => candidate.match(item.tool!));
      return renderer ? <PluginRendererBoundary><div className="plugin-tool-renderer"><PluginToolContent renderer={renderer} tool={item.tool} /></div></PluginRendererBoundary> : <ToolCard tool={item.tool} />;
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

export function Conversation({ items, queue, empty }: { items: Item[]; queue?: QueueStatus; empty: React.ReactNode }) {
  const ref = useRef<HTMLDivElement | null>(null);
  const [pinned, setPinned] = useState(true);
  const {toolRenderers, attachmentRenderers} = usePluginContributions();

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
          <Row key={itemKey(item)} item={item} toolRenderers={toolRenderers} attachmentRenderers={attachmentRenderers} />
        ))}
        {queue && ((queue.steer?.length ?? 0) > 0 || (queue.followUps?.length ?? 0) > 0) ? (
          <section className="pending-queue" aria-label="Pending messages">
            <header>Pending</header>
            {(queue.steer ?? []).map((message) => (
              <article className="queued-message" key={`steer:${message.id}`}>
                <span>Steer</span>
                <pre>{message.text}</pre>
              </article>
            ))}
            {(queue.followUps ?? []).map((message) => (
              <article className="queued-message" key={`followUp:${message.id}`}>
                <span>Follow-up</span>
                <pre>{message.text}</pre>
              </article>
            ))}
          </section>
        ) : null}
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
