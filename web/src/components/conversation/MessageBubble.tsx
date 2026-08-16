import { Button } from '@/components/ui/button';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { Markdown } from '@/components/markdown/Markdown';
import { PluginBoundary } from '@/components/plugins/PluginBoundary';
import { PluginRenderContent } from '@/components/plugins/PluginRenderContent';
import { PluginSlotView } from '@/components/plugins/PluginSlotView';
import type { ContributionContext } from '@/plugin-contributions';
import { usePluginContributions } from '@/plugin-contributions';
import type { MessageItem } from '@/protocol.gen';
import { clip } from '@/safety';
import { useLayoutEffect, useRef } from 'react';
import { Download } from 'lucide-react';

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

export function MessageBubble({ message, attachmentRenderers, contributionContext }: {
  message: MessageItem;
  attachmentRenderers: ReturnType<typeof usePluginContributions>['attachmentRenderers'];
  contributionContext?: ContributionContext;
}) {
  const isUser = message.role === 'user';
  const canDownload = !isUser && !message.streaming;
  const previousStreamRef = useRef({ id: message.id, textLength: message.text.length, phase: false });
  const previousStream = previousStreamRef.current;
  const sameStream = previousStream.id === message.id;
  const appended = message.streaming && sameStream && message.text.length > previousStream.textLength;
  const animateFrom = appended ? previousStream.textLength : undefined;
  const animationPhase = previousStream.phase ? 'b' : 'a';

  useLayoutEffect(() => {
    previousStreamRef.current = {
      id: message.id,
      textLength: message.text.length,
      phase: appended ? !previousStream.phase : previousStream.phase,
    };
  }, [appended, message.id, message.text.length, previousStream.phase]);

  return (
    <article className={`msg msg-${isUser ? 'user' : 'assistant'}${canDownload ? ' msg-downloadable' : ''}`} aria-label={`${message.role} message`}>
      {canDownload ? (
        <div className="msg-actions">
          {contributionContext ? <PluginSlotView slot="assistant-message.actions" context={{ ...contributionContext, message }} /> : null}
          <Tooltip>
            <TooltipTrigger render={
              <Button type="button" size="icon-sm" variant="ghost" className="msg-download"
                aria-label="Download message as Markdown" onClick={() => downloadMarkdown(message)}>
                <Download aria-hidden="true" />
              </Button>
            } />
            <TooltipContent>Download as Markdown</TooltipContent>
          </Tooltip>
        </div>
      ) : null}
      {message.attachments?.length ? (
        <div className="message-attachments" aria-label="Message attachments">
          {message.attachments.map((attachment) => {
            const renderer = attachmentRenderers.find((candidate) => candidate.match(attachment));
            if (renderer) {
              return (
                <PluginBoundary key={attachment.id} message="Plugin renderer failed">
                  <div className="plugin-attachment-renderer"><PluginRenderContent render={() => renderer.render(attachment)} /></div>
                </PluginBoundary>
              );
            }
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
      {message.reasoning ? <div className="reasoning"><Markdown>{message.reasoning}</Markdown></div> : null}
      {message.streaming ? (
        <div className="msg-streaming" aria-live="polite">
          <Markdown animateFrom={animateFrom} animationPhase={animationPhase}>{message.text}</Markdown>
          <span className="caret" aria-hidden="true" />
        </div>
      ) : isUser ? <pre className="msg-plain">{message.text}</pre> : <Markdown>{message.text}</Markdown>}
    </article>
  );
}
