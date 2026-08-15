import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import type { ToolConfirmationRequest, ToolDecision } from '@/protocol.gen';
import { clip } from '@/safety';
import { useRef, useState } from 'react';

export function ToolConfirmDialog({ request, onDecide }: {
  request: ToolConfirmationRequest;
  onDecide: (decision: ToolDecision, reason: string) => void;
}) {
  const [reason, setReason] = useState('');
  const approveButton = useRef<HTMLButtonElement | null>(null);

  return (
    <Dialog open onOpenChange={() => undefined}>
      <DialogContent className="tool-confirm-dialog sm:max-w-xl" role="alertdialog" initialFocus={approveButton} showCloseButton={false}>
        <DialogTitle>Tool confirmation</DialogTitle>
        <DialogDescription className="sr-only">Review the requested tool call and choose whether to allow it.</DialogDescription>
        <div>
          <p>
            <strong>{clip(request.agentName, 60) || 'The agent'}</strong> wants to run{' '}
            <strong>{clip(request.displayName || request.toolName, 80)}</strong>
            {request.displayName && request.displayName !== request.toolName ? <> (<code>{clip(request.toolName, 60)}</code>)</> : null}.
          </p>
          <pre className="dialog-cmd" tabIndex={0}>{request.argsSummary}</pre>
          <p className="dialog-pattern">Always-allow would grant exactly: <code>{clip(request.pattern, 200)}</code></p>
          <p className="dialog-warning">
            Tool permissions are a tool-call policy, not an OS boundary. An approved shell command or MCP server can
            still do anything your user account can do.
          </p>
          {request.metadata ? Object.entries(request.metadata).map(([key, value]) => (
            <p key={key} className="dialog-meta">{clip(key, 60)}: {clip(value, 200)}</p>
          )) : null}
        </div>
        <div className="dialog-actions">
          <Button ref={approveButton} type="button" onClick={() => onDecide('approve', '')}>Approve once</Button>
          <Button type="button" variant="secondary" onClick={() => onDecide('approveAlways', '')}>
            {clip(request.patternLabel, 80) || 'Always allow'}
          </Button>
        </div>
        <div className="dialog-reject">
          <label htmlFor="reject-reason">Reject with a reason</label>
          <div className="row">
            <Input id="reject-reason" value={reason} onChange={(event) => setReason(event.target.value)}
              placeholder="Optional reason shown to the model" />
            <Button type="button" variant="destructive" onClick={() => onDecide('reject', reason)}>Reject</Button>
          </div>
          <div className="preset-row">
            {(request.rejectionReasons ?? []).map((item) => (
              <Button key={item.label} type="button" size="sm" variant="secondary" className="chip" onClick={() => setReason(item.reason)}>
                {clip(item.label, 40)}
              </Button>
            ))}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
