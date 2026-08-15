import { useState } from 'react';
import { Button } from '@/components/ui/button';
import type { ContributionContext } from '@/plugin-contributions';
import { usePluginContributions } from '@/plugin-contributions';
import type { ToolActivity } from '@/protocol.gen';

export function PluginToolActions({ tool, context }: {
  tool: ToolActivity;
  context?: ContributionContext;
}) {
  const { toolActions } = usePluginContributions();
  const [running, setRunning] = useState<string | null>(null);
  if (!context) return null;

  const matching = toolActions.filter((action) => {
    try { return action.match(tool, context); } catch (cause) {
      console.error(`Plugin tool action ${action._key} failed to match`, cause);
      return false;
    }
  });
  if (matching.length === 0) return null;

  return (
    <div className="plugin-tool-actions">
      {matching.map((action) => (
        <Button key={action._key} type="button" size="sm" variant="ghost"
          aria-label={action.label} title={action.description ?? action.label} disabled={running === action._key}
          onClick={() => {
            setRunning(action._key);
            Promise.resolve(action.run(tool, context))
              .catch((cause) => console.error(`Plugin tool action ${action._key} failed`, cause))
              .finally(() => setRunning((current) => current === action._key ? null : current));
          }}>
          {action.icon ?? action.label}
        </Button>
      ))}
    </div>
  );
}
