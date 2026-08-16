import { PluginBoundary } from '@/components/plugins/PluginBoundary';
import { PluginRenderContent } from '@/components/plugins/PluginRenderContent';
import { PluginToolActions } from '@/components/plugins/PluginToolActions';
import { ToolCard } from '@/components/tools/ToolCard';
import type { ContributionContext } from '@/plugin-contributions';
import { usePluginContributions } from '@/plugin-contributions';
import type { Item } from '@/protocol.gen';
import { memo, useRef } from 'react';
import { cn } from '@/lib/utils';
import { MessageBubble } from './MessageBubble';
import { NoticeCard } from './NoticeCard';
import { SummaryCard } from './SummaryCard';
import { TransferCard } from './TransferCard';

export const ConversationRow = memo(function ConversationRow({ item, entering = false, toolRenderers, attachmentRenderers, contributionContext }: {
  item: Item;
  entering?: boolean;
  toolRenderers: ReturnType<typeof usePluginContributions>['toolRenderers'];
  attachmentRenderers: ReturnType<typeof usePluginContributions>['attachmentRenderers'];
  contributionContext?: ContributionContext;
}) {
  // Tool rows often receive an update immediately after they mount. Keep the
  // entry class latched so that follow-up SSE renders cannot cancel the CSS
  // animation before its first visible frame.
  const animateEntry = useRef(entering).current;
  let content;
  switch (item.kind) {
    case 'message':
      content = item.message ? <MessageBubble message={item.message} attachmentRenderers={attachmentRenderers} contributionContext={contributionContext} /> : null;
      break;
    case 'tool': {
      if (!item.tool) break;
      const renderer = toolRenderers.find((candidate) => candidate.match(item.tool!));
      content = renderer ? (
        <PluginBoundary message="Plugin renderer failed">
          <div className="plugin-tool-renderer"><PluginRenderContent render={() => renderer.render(item.tool!)} /></div>
        </PluginBoundary>
      ) : <ToolCard tool={item.tool} actions={<PluginToolActions tool={item.tool} context={contributionContext} />} />;
      break;
    }
    case 'transfer':
      content = item.transfer ? <TransferCard transfer={item.transfer} /> : null;
      break;
    case 'notice':
      content = item.notice ? <NoticeCard notice={item.notice} /> : null;
      break;
    case 'summary':
      content = item.summary ? <SummaryCard summary={item.summary} /> : null;
      break;
  }
  return content ? <div className={cn('conversation-row', animateEntry && 'conversation-row-enter')}>{content}</div> : null;
});
