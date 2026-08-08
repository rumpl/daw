import { useRef } from 'react';
import { clip } from '../safety';

const suggestions = [
  {
    title: 'Understand this codebase',
    prompt: 'Give me a tour of this codebase: explain the architecture, key files, and how the pieces fit together.',
  },
  {
    title: 'Find something to improve',
    prompt: 'Review this project and suggest the highest-impact improvement we could make.',
  },
  {
    title: 'Run the test suite',
    prompt: 'Run the test suite, investigate any failures, and explain the results.',
  },
  {
    title: 'Review recent changes',
    prompt: 'Review the current git changes for bugs, regressions, and opportunities to simplify the code.',
  },
];

interface NewChatPromptProps {
  workspaceLabel: string;
  workspacePath: string;
  message: string;
  busy: boolean;
  onMessageChange: (message: string) => void;
  onSubmit: (message: string) => void;
}

export function NewChatPrompt({
  workspaceLabel,
  workspacePath,
  message,
  busy,
  onMessageChange,
  onSubmit,
}: NewChatPromptProps) {
  const inputRef = useRef<HTMLTextAreaElement | null>(null);

  const submit = () => {
    const trimmed = message.trim();
    if (!trimmed || busy) return;
    onSubmit(trimmed);
  };

  return (
    <section className="new-chat-prompt" aria-labelledby="new-chat-title">
      <header className="new-chat-intro">
        <div className="new-chat-mark" aria-hidden="true">
          <svg viewBox="0 0 24 24" focusable="false">
            <path className="new-chat-mark-prompt" d="m7 7 4 5-4 5" />
            <path d="M13 17h5" />
          </svg>
        </div>
        <div>
          <p className="new-chat-status"><span aria-hidden="true" /> Workspace ready</p>
          <h2 id="new-chat-title">Start something new</h2>
          <p>Ask a question, plan a feature, or hand off a task in <code>{clip(workspaceLabel, 40)}</code>.</p>
        </div>
      </header>

      <form
        className="new-chat-form"
        onSubmit={(event) => {
          event.preventDefault();
          submit();
        }}
      >
        <label className="sr-only" htmlFor="new-chat-input">What would you like to work on?</label>
        <div className="new-chat-input-row">
          <textarea
            id="new-chat-input"
            ref={inputRef}
            rows={3}
            value={message}
            disabled={busy}
            placeholder="Describe what you would like to build, fix, or understand…"
            onChange={(event) => onMessageChange(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && !event.shiftKey) {
                event.preventDefault();
                event.currentTarget.form?.requestSubmit();
              }
            }}
          />
          <button type="submit" className="primary new-chat-submit" disabled={busy || !message.trim()}>
            {busy ? 'Starting…' : 'Start chat'}
            {!busy ? <span aria-hidden="true">→</span> : null}
          </button>
        </div>
        <div className="new-chat-meta">
          <span className="new-chat-path" title={workspacePath}>{clip(workspacePath, 72)}</span>
          <span><span className="kbd">Enter</span> send · <span className="kbd">Shift Enter</span> newline</span>
        </div>
      </form>

      <div className="prompt-suggestions">
        <p>Or start with a suggestion</p>
        <div className="prompt-suggestion-grid">
          {suggestions.map((suggestion) => (
            <button
              type="button"
              key={suggestion.title}
              disabled={busy}
              onClick={() => {
                onMessageChange(suggestion.prompt);
                requestAnimationFrame(() => inputRef.current?.focus());
              }}
            >
              <span>{suggestion.title}</span>
              <span aria-hidden="true">↗</span>
            </button>
          ))}
        </div>
      </div>
    </section>
  );
}
