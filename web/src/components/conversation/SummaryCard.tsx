import { Button } from '@/components/ui/button';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible';
import type { Summary } from '@/protocol.gen';
import { ChevronRight } from 'lucide-react';

export function SummaryCard({ summary }: { summary: Summary }) {
  return (
    <Collapsible className="summary">
      <CollapsibleTrigger render={
        <Button type="button" size="sm" variant="ghost" className="summary-trigger">
          <ChevronRight className="summary-chevron" aria-hidden="true" /> Compacted history
        </Button>
      } />
      <CollapsibleContent><pre className="summary-body">{summary.text}</pre></CollapsibleContent>
    </Collapsible>
  );
}
