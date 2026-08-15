import { PluginBoundary } from '@/components/plugins/PluginBoundary';
import { PluginRenderContent } from '@/components/plugins/PluginRenderContent';
import { ToolCard } from '@/components/tools/ToolCard';
import type { ContributionContext } from '@/plugin-contributions';
import { usePluginContributions } from '@/plugin-contributions';
import type { Item } from '@/protocol.gen';
import { memo } from 'react';
import { MessageBubble } from './MessageBubble';
import { NoticeCard } from './NoticeCard';
import { SummaryCard } from './SummaryCard';
import { TransferCard } from './TransferCard';

export const ConversationRow = memo(function ConversationRow({ item, toolRenderers, attachmentRenderers, contributionContext }: {
  item: Item;
  toolRenderers: ReturnType<typeof usePluginContributions>['toolRenderers'];
  attachmentRenderers: ReturnType<typeof usePluginContributions>['attachmentRenderers'];
  contributionContext?: ContributionContext;
}) {
  switch (item.kind) {
    case 'message':
      return item.message ? <MessageBubble message={item.message} attachmentRenderers={attachmentRenderers} contributionContext={contributionContext} /> : null;
    case 'tool': {
      if (!item.tool) return null;
      const renderer = toolRenderers.find((candidate) => candidate.match(item.tool!));
      return renderer ? (
        <PluginBoundary message="Plugin renderer failed">
          <div className="plugin-tool-renderer"><PluginRenderContent render={() => renderer.render(item.tool!)} /></div>
        </PluginBoundary>
      ) : <ToolCard tool={item.tool} />;
    }
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
