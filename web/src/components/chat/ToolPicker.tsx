import { Button } from '@/components/ui/button';
import { Dialog, DialogClose, DialogContent, DialogTitle, DialogTrigger } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { ScrollArea } from '@/components/ui/scroll-area';
import type { ToolOption } from '@/protocol.gen';
import { clip } from '@/safety';
import { Search, Wrench, X } from 'lucide-react';
import { useMemo, useState } from 'react';

export function ToolPicker({ tools, disabled, onChange }: {
  tools: ToolOption[];
  disabled: boolean;
  onChange: (name: string, enabled: boolean) => void;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const filtered = useMemo(() => {
    const terms = query.toLowerCase().split(/\s+/).filter(Boolean);
    return tools.filter((tool) => {
      const text = `${tool.name} ${tool.category ?? ''} ${tool.description ?? ''}`.toLowerCase();
      return terms.every((term) => text.includes(term));
    });
  }, [query, tools]);
  const enabledCount = tools.filter((tool) => tool.enabled).length;

  return (
    <Dialog open={open} onOpenChange={(next) => { setOpen(next); if (!next) setQuery(''); }}>
      <DialogTrigger render={
        <Button type="button" variant="secondary" className="tool-picker-trigger" disabled={disabled}
          aria-label={`Tools: ${enabledCount} of ${tools.length} enabled`} />
      }>
        <Wrench aria-hidden="true" /> Tools {enabledCount}/{tools.length}
      </DialogTrigger>
      <DialogContent className="tool-picker-dialog sm:max-w-xl" showCloseButton={false}>
        <div className="tool-picker-head">
          <DialogTitle>Agent tools</DialogTitle>
          <DialogClose render={<Button type="button" size="icon-sm" variant="ghost" aria-label="Close tool picker" />}>
            <X aria-hidden="true" />
          </DialogClose>
        </div>
        <p className="tool-picker-help">Disabled tools are not offered to the model. Changes apply to this session and require the agent to be idle.</p>
        <label className="tool-picker-search">
          <span className="sr-only">Search tools</span>
          <Search aria-hidden="true" />
          <Input value={query} placeholder="Search tools…" onChange={(event) => setQuery(event.target.value)} />
        </label>
        <ScrollArea className="tool-picker-list">
          {filtered.length === 0 ? <p className="tool-picker-empty">No matching tools.</p> : (
            <ul>
              {filtered.map((tool) => (
                <li key={tool.name}>
                  <label>
                    <input type="checkbox" checked={tool.enabled} disabled={disabled}
                      onChange={(event) => onChange(tool.name, event.target.checked)} />
                    <span className="tool-picker-info">
                      <strong>{clip(tool.name, 80)}</strong>
                      {tool.category ? <small>{clip(tool.category, 40)}</small> : null}
                      {tool.description ? <span>{clip(tool.description, 180)}</span> : null}
                    </span>
                  </label>
                </li>
              ))}
            </ul>
          )}
        </ScrollArea>
      </DialogContent>
    </Dialog>
  );
}
