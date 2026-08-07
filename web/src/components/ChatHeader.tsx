import { useEffect, useRef, useState, type RefObject } from 'react';
import type { ModelOption, UpdateConfigRequest } from '../protocol.gen';
import type { ChatState, ConnectionState } from '../reducer';
import { clip, formatCost, formatTokens } from '../safety';
import { ModelPicker } from './ModelPicker';

interface ChatHeaderProps {
  hasChat: boolean;
  state: ChatState;
  connection: ConnectionState;
  models: ModelOption[];
  busyAction: boolean;
  menuButton: RefObject<HTMLButtonElement | null>;
  drawerOpen: boolean;
  onToggleDrawer: () => void;
  onPatchConfig: (patch: Pick<UpdateConfigRequest, 'model' | 'thinkingLevel'>) => void;
  onCompact: () => void;
  onRename: (title: string) => void;
}

export function ChatHeader({
  hasChat,
  state,
  connection,
  models,
  busyAction,
  menuButton,
  drawerOpen,
  onToggleDrawer,
  onPatchConfig,
  onCompact,
  onRename,
}: ChatHeaderProps) {
  const [controlsOpen, setControlsOpen] = useState(false);
  const settingsButton = useRef<HTMLButtonElement | null>(null);
  const busy = state.run.state !== 'idle';
  const connectionLabel =
    connection === 'connected'
      ? 'Connected'
      : connection === 'reconnecting'
        ? 'Reconnecting…'
        : connection === 'connecting'
          ? 'Connecting…'
          : 'Disconnected';

  useEffect(() => {
    if (!controlsOpen) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setControlsOpen(false);
        settingsButton.current?.focus();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [controlsOpen]);

  const patchConfig = (patch: Pick<UpdateConfigRequest, 'model' | 'thinkingLevel'>) => {
    onPatchConfig(patch);
    setControlsOpen(false);
  };

  return (
    <header className="topbar">
      <button
        type="button"
        className="menu-button"
        ref={menuButton}
        aria-expanded={drawerOpen}
        aria-controls="sidebar"
        onClick={onToggleDrawer}
      >
        Menu
      </button>

      <div className="topbar-title">
        <h1>{clip(state.meta?.title || 'docker-agent', 80)}</h1>
        {hasChat ? (
          <span className={`conn conn-${connection}`} role="status" aria-label={connectionLabel}>
            <span className="conn-dot" aria-hidden="true" />
            <span className="conn-text">{connectionLabel}</span>
          </span>
        ) : null}
      </div>

      {hasChat && state.meta ? (
        <>
          <button
            type="button"
            className="settings-toggle"
            ref={settingsButton}
            aria-expanded={controlsOpen}
            aria-controls="chat-controls"
            onClick={() => setControlsOpen((open) => !open)}
          >
            Settings
          </button>

          {controlsOpen ? (
            <div className="controls-scrim" role="presentation" onClick={() => setControlsOpen(false)} />
          ) : null}

          <div id="chat-controls" className={`controls ${controlsOpen ? 'open' : ''}`}>
            <ModelPicker
              models={models}
              current={state.meta.model}
              disabled={busy || busyAction || models.length === 0}
              onSelect={(model) => patchConfig({ model })}
            />

            <label>
              <span className="sr-only">Thinking budget</span>
              <select
                value={state.meta.thinkingLevel}
                disabled={busy || busyAction || (state.meta.thinkingLevels ?? []).length === 0}
                onChange={(event) => patchConfig({ thinkingLevel: event.target.value })}
              >
                {(state.meta.thinkingLevels ?? []).length === 0 ? <option value="">thinking: n/a</option> : null}
                {state.meta.thinkingLevel && !(state.meta.thinkingLevels ?? []).includes(state.meta.thinkingLevel) ? (
                  <option value={state.meta.thinkingLevel}>thinking: {clip(state.meta.thinkingLevel, 20)}</option>
                ) : null}
                {(state.meta.thinkingLevels ?? []).map((level) => (
                  <option key={level} value={level}>
                    thinking: {level}
                  </option>
                ))}
              </select>
            </label>


            <div className="control-row">
              <button type="button" onClick={onCompact} disabled={busy || busyAction}>
                Compact
              </button>
              <button
                type="button"
                onClick={() => {
                  const title = window.prompt('New title', state.meta?.title ?? '');
                  if (title) onRename(title);
                }}
                disabled={busyAction}
              >
                Rename
              </button>
            </div>

            <span className="usage">
              {formatTokens(state.usage.inputTokens + state.usage.outputTokens)} tokens · {formatCost(state.usage.cost)}
            </span>
          </div>
        </>
      ) : null}
    </header>
  );
}
