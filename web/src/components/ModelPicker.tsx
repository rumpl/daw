import { useEffect, useMemo, useRef, useState } from 'react';
import type { ModelOption } from '../protocol.gen';
import { clip } from '../safety';

/** formatContext renders a context window as a compact token count. */
function formatContext(n: number): string {
  if (!n) return '';
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(n % 1_000_000 === 0 ? 0 : 1)}M ctx`;
  if (n >= 1000) return `${Math.round(n / 1000)}k ctx`;
  return `${n} ctx`;
}

/** formatCost renders USD per 1M tokens, the unit docker-agent reports. */
function formatCost(input: number, output: number): string {
  if (!input && !output) return '';
  const fmt = (v: number) => (v >= 1 ? `$${v.toFixed(2)}` : `$${v.toFixed(3)}`);
  return `${fmt(input)} / ${fmt(output)} per 1M`;
}

interface Group {
  label: string;
  models: ModelOption[];
}

/**
 * groupModels organises the runtime's model list into readable sections:
 * models the agent config names, models already used in this session, then the
 * provider catalog grouped by provider. Order within a group is preserved as
 * the runtime returned it.
 */
function groupModels(models: ModelOption[]): Group[] {
  const configured: ModelOption[] = [];
  const custom: ModelOption[] = [];
  const byProvider = new Map<string, ModelOption[]>();

  for (const m of models) {
    if (!m.isCatalog && !m.isCustom) {
      configured.push(m);
    } else if (m.isCustom) {
      custom.push(m);
    } else {
      const key = m.provider || 'other';
      const list = byProvider.get(key);
      if (list) list.push(m);
      else byProvider.set(key, [m]);
    }
  }

  const groups: Group[] = [];
  if (configured.length) groups.push({ label: 'From this agent', models: configured });
  if (custom.length) groups.push({ label: 'Used in this session', models: custom });
  for (const provider of [...byProvider.keys()].sort()) {
    groups.push({ label: provider, models: byProvider.get(provider) ?? [] });
  }
  return groups;
}

function matches(m: ModelOption, query: string): boolean {
  if (!query) return true;
  const haystack = `${m.name} ${m.ref} ${m.provider} ${m.model} ${m.family}`.toLowerCase();
  return query
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean)
    .every((term) => haystack.includes(term));
}

/**
 * ModelPicker replaces a 150-entry native select with a searchable, grouped
 * list. The trigger shows the current model; the dialog supports type-to-filter
 * and full keyboard navigation.
 */
export function ModelPicker({
  models,
  current,
  disabled,
  onSelect,
}: {
  models: ModelOption[];
  current: string;
  disabled: boolean;
  onSelect: (ref: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const trigger = useRef<HTMLButtonElement | null>(null);
  const search = useRef<HTMLInputElement | null>(null);
  const listRef = useRef<HTMLDivElement | null>(null);

  const groups = useMemo(() => {
    const filtered = models.filter((m) => matches(m, query));
    return groupModels(filtered);
  }, [models, query]);

  // Flat order drives keyboard navigation across group boundaries.
  const flat = useMemo(() => groups.flatMap((g) => g.models), [groups]);

  useEffect(() => {
    setActive(0);
  }, [query]);

  useEffect(() => {
    if (!open) return;
    search.current?.focus();
    const idx = flat.findIndex((m) => m.ref === current);
    if (idx >= 0) setActive(idx);
  }, [open, current, flat]);

  useEffect(() => {
    if (!open) return;
    listRef.current?.querySelector<HTMLElement>('[data-active="true"]')?.scrollIntoView({ block: 'nearest' });
  }, [active, open]);

  const close = () => {
    setOpen(false);
    setQuery('');
    trigger.current?.focus();
  };

  const choose = (ref: string) => {
    close();
    if (ref !== current) onSelect(ref);
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      // Dismiss only this layer: without stopping propagation the mobile
      // settings sheet's own Escape handler would close underneath us.
      e.stopPropagation();
      close();
      return;
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setActive((i) => (flat.length ? (i + 1) % flat.length : 0));
      return;
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActive((i) => (flat.length ? (i - 1 + flat.length) % flat.length : 0));
      return;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      const picked = flat[active];
      if (picked) choose(picked.ref);
    }
  };

  const currentModel = models.find((m) => m.ref === current);
  const label = clip(currentModel?.name || currentModel?.model || current || 'model', 40);

  return (
    <>
      <button
        type="button"
        ref={trigger}
        className="model-trigger"
        disabled={disabled}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={`Model: ${label}`}
        onClick={() => setOpen(true)}
      >
        {label}
      </button>

      {open ? (
        <div className="dialog-scrim" role="presentation" onClick={close}>
          <div
            className="dialog model-dialog"
            role="dialog"
            aria-modal="true"
            aria-label="Choose a model"
            onClick={(e) => e.stopPropagation()}
            onKeyDown={onKeyDown}
          >
            <div className="model-search">
              <label className="sr-only" htmlFor="model-search-input">
                Search models
              </label>
              <input
                id="model-search-input"
                ref={search}
                value={query}
                placeholder="Search models…"
                autoComplete="off"
                spellCheck={false}
                onChange={(e) => setQuery(e.target.value)}
              />
              <span className="model-count">
                {flat.length} of {models.length}
              </span>
            </div>

            <div className="model-list" ref={listRef} role="listbox" aria-label="Models">
              {flat.length === 0 ? <p className="model-empty">No model matches “{clip(query, 40)}”.</p> : null}
              {groups.map((group) => (
                <section key={group.label}>
                  <h3 className="model-group">{clip(group.label, 40)}</h3>
                  {group.models.map((m) => {
                    const index = flat.indexOf(m);
                    const isCurrent = m.ref === current;
                    return (
                      <button
                        key={m.ref}
                        type="button"
                        role="option"
                        aria-selected={isCurrent}
                        data-active={index === active}
                        className={`model-row${index === active ? ' active' : ''}${isCurrent ? ' current' : ''}`}
                        onMouseEnter={() => setActive(index)}
                        onClick={() => choose(m.ref)}
                      >
                        <span className="model-main">
                          <span className="model-name">{clip(m.name || m.model, 60)}</span>
                          {m.isDefault ? <span className="model-tag">agent default</span> : null}
                          {isCurrent ? <span className="model-tag current-tag">current</span> : null}
                        </span>
                        <span className="model-ref">{clip(m.ref, 70)}</span>
                        <span className="model-facts">
                          {formatContext(m.contextLimit)}
                          {m.contextLimit && (m.inputCost || m.outputCost) ? ' · ' : ''}
                          {formatCost(m.inputCost, m.outputCost)}
                        </span>
                      </button>
                    );
                  })}
                </section>
              ))}
            </div>

            <div className="model-foot">
              <span>
                <span className="kbd">↑</span>
                <span className="kbd">↓</span> move · <span className="kbd">Enter</span> select ·{' '}
                <span className="kbd">Esc</span> close
              </span>
              <button type="button" onClick={close}>
                Cancel
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </>
  );
}
