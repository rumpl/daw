import { Button } from '@/components/ui/button';
import type { ContributionContext, PluginActionLocation } from '@/plugin-contributions';
import { usePluginContributions } from '@/plugin-contributions';

export function PluginActionButtons({ location, context }: {
  location: PluginActionLocation;
  context: ContributionContext;
}) {
  const { actions } = usePluginContributions();
  const matching = actions.filter((action) => action.locations.includes(location) && (!action.when || action.when(context)));
  if (matching.length === 0) return null;

  return (
    <div className={`plugin-actions plugin-actions-${location}`}>
      {matching.map((action) => (
        <Button type="button" variant="secondary" key={action._key} title={action.description || action.label}
          onClick={() => void action.run(context)}>{action.label}</Button>
      ))}
    </div>
  );
}
