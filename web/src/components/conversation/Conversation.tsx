import { ArrowDown } from 'lucide-react';
import { Button } from '@/components/ui/button';
import type { ContributionContext } from '@/plugin-contributions';
import { usePluginContributions } from '@/plugin-contributions';
import type { Item, QueueStatus } from '@/protocol.gen';
import { itemKey } from '@/reducer';
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type MouseEvent as ReactMouseEvent } from 'react';
import { AutoHeightContainer } from './AutoHeightContainer';
import { ConversationRow } from './ConversationRow';
import { PendingQueue } from './PendingQueue';

const BOTTOM_THRESHOLD_PX = 96;
const HISTORY_BATCH_SIZE = 80;
const HISTORY_LOAD_THRESHOLD_PX = 240;

export function Conversation({ items, queue, empty, contributionContext }: {
  items: Item[];
  queue?: QueueStatus;
  empty: React.ReactNode;
  contributionContext?: ContributionContext;
}) {
  const ref = useRef<HTMLDivElement | null>(null);
  const scrollWrapRef = useRef<HTMLDivElement | null>(null);
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
  const userMessages = useMemo(() => items.filter((item) => (
    item.kind === 'message'
    && item.message?.role === 'user'
    && Boolean(
      item.message.text.trim()
      || item.message.reasoning.trim()
      || item.message.attachments?.length
      || item.message.streaming
    )
  )), [items]);
  const userMessageIndexes = useMemo(() => new Map(
    userMessages.map((item, index) => [item.message!.id, index]),
  ), [userMessages]);
  const [pinned, setPinned] = useState(true);
  const [visibleItemCount, setVisibleItemCount] = useState(() => Math.min(items.length, HISTORY_BATCH_SIZE));
  const historyIdentityRef = useRef(conversationIdentity);
  const historyScrollRef = useRef<{ height: number; top: number } | null>(null);
  const visibleCount = historyIdentityRef.current === conversationIdentity
    ? Math.min(items.length, Math.max(HISTORY_BATCH_SIZE, visibleItemCount))
    : Math.min(items.length, HISTORY_BATCH_SIZE);
  const visibleStart = Math.max(0, items.length - visibleCount);
  const visibleItems = items.slice(visibleStart);
  const toolToggleScrollRef = useRef<{ top: number; timer: number } | null>(null);
  const { toolRenderers, attachmentRenderers } = usePluginContributions();

  useLayoutEffect(() => {
    renderedItemsRef.current = { identity: conversationIdentity, keys: new Set(itemKeys) };
  });

  useLayoutEffect(() => {
    if (historyIdentityRef.current !== conversationIdentity) {
      historyIdentityRef.current = conversationIdentity;
      setVisibleItemCount(Math.min(items.length, HISTORY_BATCH_SIZE));
    } else if (items.length === 0) {
      setVisibleItemCount(0);
    }
  }, [conversationIdentity, items.length]);

  useLayoutEffect(() => {
    const element = ref.current;
    const preserved = historyScrollRef.current;
    if (!element || !preserved) return;
    const frame = window.requestAnimationFrame(() => {
      element.scrollTop = preserved.top + element.scrollHeight - preserved.height;
      historyScrollRef.current = null;
    });
    return () => window.cancelAnimationFrame(frame);
  }, [visibleCount]);

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

  const loadEarlierItems = () => {
    const element = ref.current;
    if (!element || visibleStart === 0 || historyScrollRef.current) return;
    historyScrollRef.current = { height: element.scrollHeight, top: element.scrollTop };
    setVisibleItemCount((current) => Math.min(items.length, Math.max(HISTORY_BATCH_SIZE, current) + HISTORY_BATCH_SIZE));
  };

  const onScroll = () => {
    const element = ref.current;
    if (!element) return;
    const distance = element.scrollHeight - element.scrollTop - element.clientHeight;
    setPinned(distance <= BOTTOM_THRESHOLD_PX);
    if (element.scrollTop <= HISTORY_LOAD_THRESHOLD_PX) loadEarlierItems();
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

  const updateMarkerProximity = (clientX: number, clientY: number) => {
    const wrap = scrollWrapRef.current;
    if (!wrap) return;
    const bounds = wrap.getBoundingClientRect();
    const horizontalDistance = Math.max(0, bounds.right - clientX);
    const horizontalRange = 260;
    const linearProximity = Math.max(0, Math.min(1, 1 - horizontalDistance / horizontalRange));
    const horizontalProximity = Math.pow(linearProximity, 0.45);

    const markers = Array.from(wrap.querySelectorAll<HTMLElement>('.message-jump-marker'));
    const markerMetrics = markers.map((marker) => {
      const markerBounds = marker.getBoundingClientRect();
      const distance = Math.abs(clientY - (markerBounds.top + markerBounds.height / 2));
      return { marker, distance, verticalProximity: Math.max(0, 1 - distance / 52) };
    });
    const closestDistance = Math.min(...markerMetrics.map(({ distance }) => distance));

    markerMetrics.forEach(({ marker, distance, verticalProximity }) => {
      const influence = horizontalProximity * verticalProximity;
      const selectedBoost = distance === closestDistance ? horizontalProximity * 0.75 : 0;
      marker.style.setProperty('--marker-opacity', String(horizontalProximity * (0.55 + verticalProximity * 0.45)));
      marker.style.setProperty('--marker-scale', String(1 + influence * 1.35 + selectedBoost));
    });
  };

  const hideMarkers = () => {
    scrollWrapRef.current?.querySelectorAll<HTMLElement>('.message-jump-marker').forEach((marker) => {
      marker.style.removeProperty('--marker-opacity');
      marker.style.removeProperty('--marker-scale');
    });
  };

  const pendingJumpRef = useRef<number | null>(null);
  useLayoutEffect(() => {
    const index = pendingJumpRef.current;
    if (index === null) return;
    const element = ref.current?.querySelector<HTMLElement>(`[data-user-message-index="${index}"]`);
    if (!element) return;
    pendingJumpRef.current = null;
    element.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }, [visibleCount]);

  const jumpToUserMessage = (index: number) => {
    const element = ref.current?.querySelector<HTMLElement>(`[data-user-message-index="${index}"]`);
    if (element) {
      element.scrollIntoView({ behavior: 'smooth', block: 'start' });
      return;
    }
    const itemIndex = items.findIndex((item) => item.message?.id === userMessages[index]?.message?.id);
    if (itemIndex < 0) return;
    pendingJumpRef.current = index;
    setVisibleItemCount(items.length - itemIndex);
  };

  return (
    <div className="conversation-wrap">
      <div className="conversation-scroll" ref={scrollWrapRef}
        onPointerMove={(event) => updateMarkerProximity(event.clientX, event.clientY)}
        onPointerLeave={hideMarkers}>
        <div className="conversation" ref={ref} onScroll={onScroll} onClickCapture={preserveScrollWhileTogglingTool}
          role="log" aria-live="polite" aria-label="Conversation">
          {items.length === 0 ? <div className="empty">{empty}</div> : null}
          <AutoHeightContainer onHeightChange={keepPinnedToBottom}>
            {visibleStart > 0 ? (
              <button type="button" className="conversation-load-earlier" onClick={loadEarlierItems}>
                Load earlier messages
              </button>
            ) : null}
            {visibleItems.map((item) => (
              <ConversationRow key={itemKey(item)} item={item} entering={enteringKeys.has(itemKey(item))}
                userMessageIndex={item.message ? userMessageIndexes.get(item.message.id) : undefined}
                toolRenderers={toolRenderers} attachmentRenderers={attachmentRenderers}
                contributionContext={contributionContext} />
            ))}
          </AutoHeightContainer>
        </div>
        {userMessages.length > 0 ? (
          <nav className="message-jump-nav" aria-label="User messages">
            {userMessages.map((item, index) => (
              <button key={item.message!.id} type="button" className="message-jump-marker"
                aria-label={`Jump to user message ${index + 1}`}
                title={item.message!.text.trim() || `User message ${index + 1}`}
                onClick={() => jumpToUserMessage(index)} />
            ))}
          </nav>
        ) : null}
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
