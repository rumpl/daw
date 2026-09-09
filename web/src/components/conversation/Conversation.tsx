import { ArrowDown } from 'lucide-react';
import { Button } from '@/components/ui/button';
import type { ContributionContext } from '@/plugin-contributions';
import { usePluginContributions } from '@/plugin-contributions';
import type { Item, QueueStatus } from '@/protocol.gen';
import { itemKey } from '@/reducer';
import { useEffect, useLayoutEffect, useRef, useState, type MouseEvent as ReactMouseEvent } from 'react';
import { AutoHeightContainer } from './AutoHeightContainer';
import { ConversationRow } from './ConversationRow';
import { PendingQueue } from './PendingQueue';

const BOTTOM_THRESHOLD_PX = 96;

export function Conversation({ items, queue, empty, contributionContext }: {
  items: Item[];
  queue?: QueueStatus;
  empty: React.ReactNode;
  contributionContext?: ContributionContext;
}) {
  const ref = useRef<HTMLDivElement | null>(null);
  const conversationIdentity = contributionContext?.chatId ?? null;
  const itemKeys = items.map(itemKey);
  const renderedItemsRef = useRef({
    identity: conversationIdentity,
    keys: new Set(itemKeys),
  });
  const enteringKeys = renderedItemsRef.current.identity === conversationIdentity
    ? new Set(itemKeys.filter((key) => !renderedItemsRef.current.keys.has(key)))
    : new Set<string>();
  const hasContent = items.length > 0
    || (queue?.steer?.length ?? 0) > 0
    || (queue?.followUps?.length ?? 0) > 0;
  const [pinned, setPinned] = useState(true);
  const toolToggleScrollRef = useRef<{ top: number; timer: number } | null>(null);
  const { toolRenderers, attachmentRenderers } = usePluginContributions();

  useLayoutEffect(() => {
    renderedItemsRef.current = { identity: conversationIdentity, keys: new Set(itemKeys) };
  });

  useEffect(() => {
    if (!hasContent) setPinned(true);
  }, [hasContent]);

  useEffect(() => () => {
    if (toolToggleScrollRef.current) window.clearTimeout(toolToggleScrollRef.current.timer);
  }, []);

  useEffect(() => {
    const element = ref.current;
    if (!element || !pinned) return;
    element.scrollTop = element.scrollHeight;
  }, [items, pinned]);

  const onScroll = () => {
    const element = ref.current;
    if (!element) return;
    const distance = element.scrollHeight - element.scrollTop - element.clientHeight;
    setPinned(distance <= BOTTOM_THRESHOLD_PX);
  };

  const keepPinnedToBottom = () => {
    const element = ref.current;
    const preserved = toolToggleScrollRef.current;
    if (element && preserved) {
      element.scrollTop = preserved.top;
    } else if (element && pinned) {
      element.scrollTop = element.scrollHeight;
    }
  };

  const preserveScrollWhileTogglingTool = (event: ReactMouseEvent<HTMLDivElement>) => {
    const target = event.target as Element;
    if (!target.closest('.tool-trigger')) return;

    const element = ref.current;
    if (!element) return;
    if (toolToggleScrollRef.current) window.clearTimeout(toolToggleScrollRef.current.timer);

    const top = element.scrollTop;
    const timer = window.setTimeout(() => {
      toolToggleScrollRef.current = null;
      const distance = element.scrollHeight - element.scrollTop - element.clientHeight;
      setPinned(distance <= BOTTOM_THRESHOLD_PX);
    }, 420);
    toolToggleScrollRef.current = { top, timer };
  };

  return (
    <div className="conversation-wrap">
      <div className="conversation-scroll">
        <div className="conversation" ref={ref} onScroll={onScroll} onClickCapture={preserveScrollWhileTogglingTool}
          role="log" aria-live="polite" aria-label="Conversation">
          {items.length === 0 ? <div className="empty">{empty}</div> : null}
          <AutoHeightContainer onHeightChange={keepPinnedToBottom}>
            {items.map((item) => (
              <ConversationRow key={itemKey(item)} item={item} entering={enteringKeys.has(itemKey(item))}
                toolRenderers={toolRenderers} attachmentRenderers={attachmentRenderers}
                contributionContext={contributionContext} />
            ))}
          </AutoHeightContainer>
        </div>
        {!pinned && hasContent ? (
          <Button type="button" variant="secondary" size="icon" className="jump"
            aria-label="Jump to latest" onClick={() => {
              setPinned(true);
              const element = ref.current;
              if (element) element.scrollTop = element.scrollHeight;
            }}>
            <ArrowDown aria-hidden="true" />
          </Button>
        ) : null}
      </div>
      {queue ? <PendingQueue queue={queue} /> : null}
    </div>
  );
}
