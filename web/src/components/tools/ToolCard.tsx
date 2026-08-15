import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible';
import type { ToolActivity } from '@/protocol.gen';
import { clip } from '@/safety';
import { ChevronRight } from 'lucide-react';
import { PlainOutput } from './PlainOutput';
import { ToolImages } from './ToolImages';
import { toolRenderers } from './toolRenderers';

const stateLabel: Record<ToolActivity['state'], string> = {
  pending: 'Pending', awaiting_confirmation: 'Waiting', running: 'Running', success: 'Done', error: 'Failed', rejected: 'Rejected',
};
const stateMark: Record<ToolActivity['state'], string> = {
  pending: '·', awaiting_confirmation: '!', running: '·', success: '✓', error: '×', rejected: '×',
};

function fallbackTitle(name: string): string {
  return name.split(/[_-]+/).filter(Boolean).map((part) => part[0]?.toUpperCase() + part.slice(1)).join(' ') || 'Tool';
}

export function ToolCard({ tool }: { tool: ToolActivity }) {
  const args = tool.arguments ?? {};
  const renderer = toolRenderers[tool.name];
  const title = tool.displayName || renderer?.title || fallbackTitle(tool.name);
  const summary = renderer?.summary(args, tool.argsSummary) || tool.argsSummary;
  const tone = tool.state === 'error' ? 'error' : tool.state === 'rejected' ? 'rejected' : tool.state === 'success' ? 'ok' : 'running';
  const body = renderer?.body(tool, args) ?? <PlainOutput tool={tool} />;

  return (
    <div className="tool-with-images">
      <Collapsible className={`tool tool-${tone} tool-kind-${tool.name}`} aria-label={`tool ${tool.name}`}>
        <CollapsibleTrigger render={
          <Button type="button" variant="ghost" className="tool-trigger">
            <ChevronRight className="tool-chevron" size={14} aria-hidden="true" />
            <span className="tool-heading">
              <span className="tool-name">{clip(title, 80)}</span>
              {!renderer && title !== tool.name ? <code className="tool-technical-name">{clip(tool.name, 60)}</code> : null}
              {summary ? <span className="tool-args" title={summary}>{clip(summary, 300)}</span> : null}
            </span>
            {tool.state !== 'success' ? (
              <Badge variant={tool.state === 'error' || tool.state === 'rejected' ? 'destructive' : 'secondary'} className="tool-state">
                <span aria-hidden="true">{stateMark[tool.state]}</span>{stateLabel[tool.state]}
              </Badge>
            ) : null}
          </Button>
        } />
        <CollapsibleContent>
          <div className="tool-body">{body}</div>
          {tool.truncated ? <p className="tool-note">Output truncated for display.</p> : null}
        </CollapsibleContent>
      </Collapsible>
      <ToolImages tool={tool} />
    </div>
  );
}
