import { useEffect, useRef, useState } from 'react';
import type { Attachment, CommandInfo, RunStatus } from '../protocol.gen';
import { clip } from '../safety';

export type SendMode = 'normal' | 'steer' | 'followUp';

/**
 * Composer: growing multiline input with a persisted draft. On desktop Enter
 * sends, Shift+Enter inserts a newline, and while a turn is running Alt+Enter
 * queues a follow-up. An explicit Send button is present while idle so touch
 * devices never depend on keyboard behaviour.
 */
export function Composer({
  draft,
  onDraftChange,
  run,
  disabled,
  commands,
  attachments,
  uploading,
  onAddAttachments,
  onRemoveAttachment,
  onSend,
  onStop,
}: {
  draft: string;
  onDraftChange: (v: string) => void;
  run: RunStatus;
  disabled: boolean;
  commands: CommandInfo[];
  attachments: Attachment[];
  uploading: boolean;
  onAddAttachments: (files: File[]) => void;
  onRemoveAttachment: (id: string) => void;
  onSend: (text: string, mode: SendMode) => void;
  onStop: () => void;
}) {
  const textarea = useRef<HTMLTextAreaElement | null>(null);
  const fileInput = useRef<HTMLInputElement | null>(null);
  const [menuIndex, setMenuIndex] = useState(0);
  const busy = run.state !== 'idle';

  useEffect(() => {
    const el = textarea.current;
    if (!el) return;
    if (!draft) {
      // Do not let a wrapping placeholder make the empty composer taller.
      el.style.height = '';
      return;
    }
    el.style.height = 'auto';
    el.style.height = `${Math.min(el.scrollHeight, 220)}px`;
  }, [draft]);

  const slash = draft.startsWith('/') && !draft.includes(' ') ? draft.slice(1).toLowerCase() : null;
  const matches = slash === null ? [] : commands.filter((c) => c.name.toLowerCase().startsWith(slash)).slice(0, 6);

  const submit = (mode: SendMode) => {
    const text = draft.trim();
    if ((!text && attachments.length === 0) || disabled || uploading) return;
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
      if (e.key === 'Tab' || (e.key === 'Enter' && !e.shiftKey && !e.altKey)) {
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
      submit(busy ? (e.altKey ? 'followUp' : 'steer') : 'normal');
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

      {attachments.length > 0 ? (
        <ul className="composer-attachments" aria-label="Attachments">
          {attachments.map((attachment) => (
            <li key={attachment.id}>
              <span>{clip(attachment.name, 60)}</span>
              <small>{Math.ceil(attachment.size / 1024)} KB</small>
              <button type="button" aria-label={`Remove ${attachment.name}`} onClick={() => onRemoveAttachment(attachment.id)}>×</button>
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
          placeholder={busy ? 'Enter to steer · Alt+Enter for follow-up…' : 'Send a message…'}
          onChange={(e) => onDraftChange(e.target.value)}
          onKeyDown={onKeyDown}
          onPaste={(event) => {
            const files = Array.from(event.clipboardData.files);
            if (files.length > 0) onAddAttachments(files);
          }}
        />

        <input
          ref={fileInput}
          className="sr-only"
          type="file"
          multiple
          accept="image/jpeg,image/png,image/gif,image/webp,application/pdf,text/*,.txt,.json,.yaml,.yml,.toml,.go,.js,.jsx,.ts,.tsx,.py,.rs,.java,.log,.md"
          onChange={(event) => {
            onAddAttachments(Array.from(event.target.files ?? []));
            event.target.value = '';
          }}
        />
        <button type="button" className="attach-btn" disabled={disabled || uploading || attachments.length >= 4} onClick={() => fileInput.current?.click()}>
          {uploading ? 'Adding…' : 'Attach'}
        </button>

        <div className="composer-actions">
          {busy ? (
            <button type="button" className="danger" onClick={onStop}>
              {run.state === 'stopping' ? 'Stopping…' : 'Stop'}
            </button>
          ) : (
            <button type="submit" className="primary send-btn" disabled={disabled || uploading || (!draft.trim() && attachments.length === 0)}>
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
        ) : busy ? (
          <span>
            <span className="kbd">Enter</span> steer · <span className="kbd">Alt</span>+
            <span className="kbd">Enter</span> follow-up · <span className="kbd">Shift</span>+
            <span className="kbd">Enter</span> newline
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
