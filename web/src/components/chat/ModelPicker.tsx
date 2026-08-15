import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogClose, DialogContent, DialogTitle, DialogTrigger } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Search, X } from 'lucide-react';
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import type { ModelOption } from '@/protocol.gen';
import { clip } from '@/safety';

function formatContext(n: number): string {
  if (!n) return '';
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(n % 1_000_000 === 0 ? 0 : 1)}M ctx`;
  if (n >= 1000) return `${Math.round(n / 1000)}k ctx`;
  return `${n} ctx`;
}

function formatCost(input: number, output: number): string {
  if (!input && !output) return '';
  const fmt = (value: number) => (value >= 1 ? `$${value.toFixed(2)}` : `$${value.toFixed(3)}`);
  return `${fmt(input)} / ${fmt(output)} per 1M`;
}

interface Group { label: string; models: ModelOption[]; }

function groupModels(models: ModelOption[]): Group[] {
  const configured: ModelOption[] = [];
  const custom: ModelOption[] = [];
  const byProvider = new Map<string, ModelOption[]>();
  for (const model of models) {
    if (!model.isCatalog && !model.isCustom) configured.push(model);
    else if (model.isCustom) custom.push(model);
    else {
      const key = model.provider || 'other';
      const list = byProvider.get(key);
      if (list) list.push(model);
      else byProvider.set(key, [model]);
    }
  }
  const groups: Group[] = [];
  if (configured.length) groups.push({ label: 'From this agent', models: configured });
  if (custom.length) groups.push({ label: 'Used in this session', models: custom });
  for (const provider of [...byProvider.keys()].sort()) groups.push({ label: provider, models: byProvider.get(provider) ?? [] });
  return groups;
}

function matches(model: ModelOption, query: string): boolean {
  if (!query) return true;
  const haystack = `${model.name} ${model.ref} ${model.provider} ${model.model} ${model.family}`.toLowerCase();
  return query.toLowerCase().split(/\s+/).filter(Boolean).every((term) => haystack.includes(term));
}

export function ModelPicker({ models, current, disabled, onSelect }: {
  models: ModelOption[];
  current: string;
  disabled: boolean;
  onSelect: (ref: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const search = useRef<HTMLInputElement | null>(null);
  const listRef = useRef<HTMLDivElement | null>(null);
  const groups = useMemo(() => groupModels(models.filter((model) => matches(model, query))), [models, query]);
  const flat = useMemo(() => groups.flatMap((group) => group.models), [groups]);

  useEffect(() => setActive(0), [query]);
  useEffect(() => {
    if (!open) return;
    const index = flat.findIndex((model) => model.ref === current);
    if (index >= 0) setActive(index);
  }, [open, current, flat]);
  useEffect(() => {
    if (open) listRef.current?.querySelector<HTMLElement>('[data-active="true"]')?.scrollIntoView({ block: 'nearest' });
  }, [active, open]);

  const setDialogOpen = (nextOpen: boolean) => {
    setOpen(nextOpen);
    if (!nextOpen) setQuery('');
  };
  const choose = (ref: string) => {
    setDialogOpen(false);
    if (ref !== current) onSelect(ref);
  };
  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key === 'Escape') {
      event.stopPropagation();
      setDialogOpen(false);
    } else if (event.key === 'ArrowDown') {
      event.preventDefault();
      setActive((index) => flat.length ? (index + 1) % flat.length : 0);
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      setActive((index) => flat.length ? (index - 1 + flat.length) % flat.length : 0);
    } else if (event.key === 'Enter') {
      event.preventDefault();
      const picked = flat[active];
      if (picked) choose(picked.ref);
    }
  };

  const currentModel = models.find((model) => model.ref === current);
  const label = clip(currentModel?.name || currentModel?.model || current || 'model', 40);

  return (
    <Dialog open={open} onOpenChange={setDialogOpen}>
      <DialogTrigger render={<Button type="button" variant="secondary" className="model-trigger" disabled={disabled} aria-label={`Model: ${label}`} />}>
        {label}
      </DialogTrigger>
      <DialogContent className="model-dialog sm:max-w-2xl" initialFocus={search} onKeyDown={onKeyDown} showCloseButton={false}>
        <DialogTitle className="sr-only">Choose a model</DialogTitle>
        <div className="model-search">
          <label className="sr-only" htmlFor="model-search-input">Search models</label>
          <div className="relative flex-1">
            <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
            <Input id="model-search-input" ref={search} value={query} className="pl-8" placeholder="Search models…"
              autoComplete="off" spellCheck={false} onChange={(event) => setQuery(event.target.value)} />
          </div>
          <span className="model-count">{flat.length} of {models.length}</span>
          <DialogClose render={<Button type="button" size="icon-sm" variant="ghost" aria-label="Close model picker" />}>
            <X aria-hidden="true" />
          </DialogClose>
        </div>

        <ScrollArea className="model-list" ref={listRef} role="listbox" aria-label="Models">
          {flat.length === 0 ? <p className="model-empty">No model matches “{clip(query, 40)}”.</p> : null}
          {groups.map((group) => (
            <section key={group.label}>
              <h3 className="model-group">{clip(group.label, 40)}</h3>
              {group.models.map((model) => {
                const index = flat.indexOf(model);
                const isCurrent = model.ref === current;
                return (
                  <Button key={model.ref} type="button" variant="ghost" role="option" aria-selected={isCurrent}
                    data-active={index === active} className={`model-row h-auto min-h-12${index === active ? ' active' : ''}${isCurrent ? ' current' : ''}`}
                    onMouseEnter={() => setActive(index)} onClick={() => choose(model.ref)}>
                    <span className="model-main">
                      <span className="model-name">{clip(model.name || model.model, 60)}</span>
                      {model.isDefault ? <Badge variant="secondary">agent default</Badge> : null}
                      {isCurrent ? <Badge>current</Badge> : null}
                    </span>
                    <span className="model-ref">{clip(model.ref, 70)}</span>
                    <span className="model-facts">
                      {formatContext(model.contextLimit)}
                      {model.contextLimit && (model.inputCost || model.outputCost) ? ' · ' : ''}
                      {formatCost(model.inputCost, model.outputCost)}
                    </span>
                  </Button>
                );
              })}
            </section>
          ))}
        </ScrollArea>

        <div className="model-foot">
          <span><span className="kbd">↑</span><span className="kbd">↓</span> move · <span className="kbd">Enter</span> select · <span className="kbd">Esc</span> close</span>
          <DialogClose render={<Button type="button" variant="secondary" />}>Cancel</DialogClose>
        </div>
      </DialogContent>
    </Dialog>
  );
}
