import { useEffect, useRef, useState } from 'react';
import type { ElicitationRequest, ToolConfirmationRequest, ToolDecision } from '../protocol.gen';
import { clip } from '../safety';

/**
 * ToolConfirmDialog shows the exact command and the exact permission pattern
 * that will be granted. The pattern string comes from the server (built by
 * docker-agent's own toolconfirm.BuildPermissionPattern) and is never
 * reconstructed here.
 */
export function ToolConfirmDialog({
  request,
  onDecide,
}: {
  request: ToolConfirmationRequest;
  onDecide: (decision: ToolDecision, reason: string) => void;
}) {
  const ref = useRef<HTMLDivElement | null>(null);
  const [reason, setReason] = useState('');

  useEffect(() => {
    ref.current?.querySelector<HTMLButtonElement>('button')?.focus();
  }, [request.toolCallId]);

  return (
    <div className="dialog-scrim" role="presentation">
      <div
        className="dialog"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="confirm-title"
        aria-describedby="confirm-body"
        ref={ref}
      >
        <h2 id="confirm-title">Tool confirmation</h2>
        <div id="confirm-body">
          <p>
            <strong>{clip(request.agentName, 60) || 'The agent'}</strong> wants to run{' '}
            <code>{clip(request.toolName, 60)}</code>.
          </p>
          <pre className="dialog-cmd" tabIndex={0}>
            {request.argsSummary}
          </pre>
          <p className="dialog-pattern">
            Always-allow would grant exactly: <code>{clip(request.pattern, 200)}</code>
          </p>
          <p className="dialog-warning">
            Tool permissions are a tool-call policy, not an OS boundary. An approved shell command or MCP server can
            still do anything your user account can do.
          </p>
          {request.metadata
            ? Object.entries(request.metadata).map(([k, v]) => (
                <p key={k} className="dialog-meta">
                  {clip(k, 60)}: {clip(v, 200)}
                </p>
              ))
            : null}
        </div>
        <div className="dialog-actions">
          <button type="button" className="primary" onClick={() => onDecide('approve', '')}>
            Approve once
          </button>
          <button type="button" onClick={() => onDecide('approveAlways', '')}>
            {clip(request.patternLabel, 80) || 'Always allow'}
          </button>
          <button type="button" onClick={() => onDecide('approveSession', '')}>
            Approve all for this session
          </button>
        </div>
        <div className="dialog-reject">
          <label htmlFor="reject-reason">Reject with a reason</label>
          <div className="row">
            <input
              id="reject-reason"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="Optional reason shown to the model"
            />
            <button type="button" className="danger" onClick={() => onDecide('reject', reason)}>
              Reject
            </button>
          </div>
          <div className="preset-row">
            {(request.rejectionReasons ?? []).map((r) => (
              <button key={r.label} type="button" className="chip" onClick={() => setReason(r.reason)}>
                {clip(r.label, 40)}
              </button>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

/** ElicitationDialog answers one MCP elicitation, correlated by its ID. */
export function ElicitationDialog({
  request,
  onAnswer,
}: {
  request: ElicitationRequest;
  onAnswer: (action: 'accept' | 'decline' | 'cancel', content: Record<string, unknown>) => void;
}) {
  const [values, setValues] = useState<Record<string, string>>({});
  const ref = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    ref.current?.querySelector<HTMLInputElement | HTMLButtonElement>('input,button')?.focus();
  }, [request.elicitationId]);

  const schema = request.schema as { properties?: Record<string, { title?: string; type?: string }> } | undefined;
  const fields = Object.entries(schema?.properties ?? {});

  return (
    <div className="dialog-scrim" role="presentation">
      <div className="dialog" role="dialog" aria-modal="true" aria-labelledby="elicit-title" ref={ref}>
        <h2 id="elicit-title">The agent needs an answer</h2>
        <p>{clip(request.message, 600)}</p>
        {request.mode === 'url' && request.url ? <p className="dialog-meta">Reference: {clip(request.url, 200)}</p> : null}
        {fields.map(([name, spec]) => (
          <div className="field" key={name}>
            <label htmlFor={`elicit-${name}`}>{clip(spec.title ?? name, 80)}</label>
            <input
              id={`elicit-${name}`}
              value={values[name] ?? ''}
              onChange={(e) => setValues((v) => ({ ...v, [name]: e.target.value }))}
            />
          </div>
        ))}
        <div className="dialog-actions">
          <button type="button" className="primary" onClick={() => onAnswer('accept', values)}>
            Send answer
          </button>
          <button type="button" onClick={() => onAnswer('decline', {})}>
            Decline
          </button>
          <button type="button" onClick={() => onAnswer('cancel', {})}>
            Cancel
          </button>
        </div>
      </div>
    </div>
  );
}
