import { Button } from '@/components/ui/button';
import type { ContributionContext } from '@/plugin-contributions';
import { usePluginContributions } from '@/plugin-contributions';
import type { Item, QueueStatus } from '@/protocol.gen';
import { itemKey } from '@/reducer';
import { useEffect, useRef, useState } from 'react';
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
  const [pinned, setPinned] = useState(true);
  const { toolRenderers, attachmentRenderers } = usePluginContributions();

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

  return (
    <div className="conversation-wrap">
      <div className="conversation" ref={ref} onScroll={onScroll} role="log" aria-live="polite" aria-label="Conversation">
        {items.length === 0 ? <div className="empty">{empty}</div> : null}
        {items.map((item) => (
          <ConversationRow key={itemKey(item)} item={item} toolRenderers={toolRenderers}
            attachmentRenderers={attachmentRenderers} contributionContext={contributionContext} />
        ))}
        {queue ? <PendingQueue queue={queue} /> : null}
      </div>
      {!pinned ? (
        <Button type="button" className="jump" onClick={() => {
          setPinned(true);
          const element = ref.current;
          if (element) element.scrollTop = element.scrollHeight;
        }}>
          Jump to latest
        </Button>
      ) : null}
    </div>
  );
}
