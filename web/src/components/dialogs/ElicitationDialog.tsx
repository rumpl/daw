import { Button } from '@/components/ui/button';
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import type { ElicitationRequest } from '@/protocol.gen';
import { clip } from '@/safety';
import { useState } from 'react';

export function ElicitationDialog({ request, onAnswer }: {
  request: ElicitationRequest;
  onAnswer: (action: 'accept' | 'decline' | 'cancel', content: Record<string, unknown>) => void;
}) {
  const [values, setValues] = useState<Record<string, string>>({});
  const schema = request.schema as { properties?: Record<string, { title?: string; type?: string }> } | undefined;
  const fields = Object.entries(schema?.properties ?? {});

  return (
    <Dialog open onOpenChange={() => undefined}>
      <DialogContent className="elicitation-dialog sm:max-w-xl" showCloseButton={false}>
        <DialogTitle>The agent needs an answer</DialogTitle>
        <DialogDescription>{clip(request.message, 600)}</DialogDescription>
        {request.mode === 'url' && request.url ? <p className="dialog-meta">Reference: {clip(request.url, 200)}</p> : null}
        {fields.map(([name, spec]) => (
          <div className="field" key={name}>
            <label htmlFor={`elicit-${name}`}>{clip(spec.title ?? name, 80)}</label>
            <Input id={`elicit-${name}`} value={values[name] ?? ''}
              onChange={(event) => setValues((current) => ({ ...current, [name]: event.target.value }))} />
          </div>
        ))}
        <div className="dialog-actions">
          <Button type="button" onClick={() => onAnswer('accept', values)}>Send answer</Button>
          <Button type="button" variant="secondary" onClick={() => onAnswer('decline', {})}>Decline</Button>
          <DialogClose render={<Button type="button" variant="ghost" onClick={() => onAnswer('cancel', {})} />}>
            Cancel
          </DialogClose>
        </div>
      </DialogContent>
    </Dialog>
  );
}
