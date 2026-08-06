import { useEffect, useRef, useState } from 'react';
import type { CommandInfo, RunStatus } from '../protocol.gen';
import { clip } from '../safety';

export type SendMode = 'normal' | 'steer' | 'followUp';

/**
 * Composer: growing multiline input with a persisted draft. On desktop Enter
 * sends and Shift+Enter inserts a newline; an explicit Send button is always
 * present so touch devices never depend on a keyboard behaviour.
 */
export function Composer({
  draft,
  onDraftChange,
  run,
  disabled,
  commands,
  onSend,
  onStop,
}: {
  draft: string;
  onDraftChange: (v: string) => void;
  run: RunStatus;
  disabled: boolean;
  commands: CommandInfo[];
  onSend: (text: string, mode: SendMode) => void;
  onStop: () => void;
}) {
  const textarea = useRef<HTMLTextAreaElement | null>(null);
  const [menuIndex, setMenuIndex] = useState(0);
  const busy = run.state !== 'idle';

  useEffect(() => {
    const el = textarea.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = `${Math.min(el.scrollHeight, 220)}px`;
  }, [draft]);

  const slash = draft.startsWith('/') && !draft.includes(' ') ? draft.slice(1).toLowerCase() : null;
  const matches = slash === null ? [] : commands.filter((c) => c.name.toLowerCase().startsWith(slash)).slice(0, 6);

  const submit = (mode: SendMode) => {
    const text = draft.trim();
    if (!text || disabled) return;
    onSend(text, mode);
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (matches.length > 0) {
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setMenuIndex((i) => (i + 1) % matches.length);
        return;
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault();
        setMenuIndex((i) => (i - 1 + matches.length) % matches.length);
        return;
      }
      if (e.key === 'Tab' || (e.key === 'Enter' && !e.shiftKey)) {
        const picked = matches[menuIndex];
        if (picked) {
          e.preventDefault();
          onDraftChange(`/${picked.name} `);
          setMenuIndex(0);
          return;
        }
      }
      if (e.key === 'Escape') {
        e.preventDefault();
        onDraftChange('');
        return;
      }
    }
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      submit(busy ? 'followUp' : 'normal');
    }
  };

  return (
    <form
      className="composer"
      onSubmit={(e) => {
        e.preventDefault();
        submit(busy ? 'followUp' : 'normal');
      }}
    >
      {matches.length > 0 ? (
        <ul className="slash-menu" role="listbox" aria-label="Commands">
          {matches.map((c, i) => (
            <li key={c.name}>
              <button
                type="button"
                role="option"
                aria-selected={i === menuIndex}
                className={i === menuIndex ? 'active' : ''}
                onClick={() => onDraftChange(`/${c.name} `)}
              >
                <strong>/{clip(c.name, 40)}</strong> <span>{clip(c.description, 80)}</span>
              </button>
            </li>
          ))}
        </ul>
      ) : null}

      <div className="composer-inner">
        <label className="sr-only" htmlFor="composer-input">
          Message
        </label>
        <textarea
          id="composer-input"
          ref={textarea}
          value={draft}
          rows={1}
          disabled={disabled}
          placeholder={busy ? 'Steer or queue a follow-up…' : 'Send a message…'}
          onChange={(e) => onDraftChange(e.target.value)}
          onKeyDown={onKeyDown}
        />

        <div className="composer-actions">
          {busy ? (
            <>
              <button type="button" onClick={() => submit('steer')} disabled={disabled || !draft.trim()}>
                Steer
              </button>
              <button type="button" onClick={() => submit('followUp')} disabled={disabled || !draft.trim()}>
                Follow-up
              </button>
              <button type="button" className="danger" onClick={onStop}>
                {run.state === 'stopping' ? 'Stopping…' : 'Stop'}
              </button>
            </>
          ) : (
            <button type="submit" className="primary send-btn" disabled={disabled || !draft.trim()}>
              Send
            </button>
          )}
        </div>
      </div>

      <div className="composer-foot">
        {busy && (run.queue.steerDepth > 0 || run.queue.followUpDepth > 0) ? (
          <span className="queue-pill" aria-live="polite">
            {run.queue.steerDepth} steer · {run.queue.followUpDepth} follow-up queued
          </span>
        ) : (
          <span>
            <span className="kbd">Enter</span> send · <span className="kbd">Shift</span>+
            <span className="kbd">Enter</span> newline · <span className="kbd">/</span> commands
          </span>
        )}
      </div>
    </form>
  );
}
