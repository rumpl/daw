import type { ContributionContext, PluginSlot } from '@/plugin-contributions';
import { usePluginContributions } from '@/plugin-contributions';
import { PluginBoundary } from './PluginBoundary';
import { PluginRenderContent } from './PluginRenderContent';

export function PluginSlotView({ slot, context, className = '' }: {
  slot: PluginSlot;
  context: ContributionContext;
  className?: string;
}) {
  const { slots } = usePluginContributions();
  const matching = slots.filter((item) => item.slot === slot);
  if (matching.length === 0) return null;

  return (
    <div className={`plugin-slot plugin-slot-${slot.replaceAll('.', '-')} ${className}`.trim()}>
      {matching.map((item) => (
        <PluginBoundary key={item._key}>
          <PluginRenderContent render={() => item.render(context)} />
        </PluginBoundary>
      ))}
    </div>
  );
}
