import { Command, CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command';
import type { ContributionContext } from '@/plugin-contributions';
import { usePluginContributions } from '@/plugin-contributions';
import { useEffect, useMemo, useState } from 'react';

export function PluginCommandPalette({ context }: { context: ContributionContext }) {
  const { actions } = usePluginContributions();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        setOpen((value) => !value);
      } else if (event.key === 'Escape') {
        setOpen(false);
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, []);

  const matches = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return actions.filter((action) => action.locations.includes('command-palette')
      && (!action.when || action.when(context))
      && (!needle || action.label.toLowerCase().includes(needle) || action.description?.toLowerCase().includes(needle)));
  }, [actions, context, query]);

  return (
    <CommandDialog open={open} onOpenChange={setOpen} title="Command palette"
      description="Search and run an action contributed by a plugin." className="plugin-command-palette sm:max-w-xl">
      <Command shouldFilter={false}>
        <CommandInput autoFocus value={query} onValueChange={setQuery} placeholder="Search plugin actions…" />
        <CommandList className="plugin-command-results">
          <CommandEmpty>No matching plugin actions.</CommandEmpty>
          <CommandGroup>
            {matches.map((action) => (
              <CommandItem key={action._key} value={action._key} onSelect={() => {
                setOpen(false);
                void action.run(context);
              }}>
                <span><strong>{action.label}</strong>{action.description ? <small>{action.description}</small> : null}</span>
              </CommandItem>
            ))}
          </CommandGroup>
        </CommandList>
      </Command>
    </CommandDialog>
  );
}
