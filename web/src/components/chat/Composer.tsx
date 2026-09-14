import { Button } from '@/components/ui/button';
import { InputGroupButton } from '@/components/ui/input-group';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { Mic, Minimize2 } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { useSpeechToText } from '@/hooks/useSpeechToText';
import type { Attachment, CommandInfo, ExecutionTarget, ExecutionTargetOption, ModelOption, RunStatus, ToolOption } from '@/protocol.gen';
import { clip, formatCost, formatTokens } from '@/safety';
import { ExecutionTargetIcon } from './ExecutionTargetIcon';
import { ModelPicker } from './ModelPicker';
import { PromptInput } from './PromptInput';
import { ToolPicker } from './ToolPicker';

export type SendMode = 'normal' | 'steer' | 'followUp';

/** A multiline message composer with attachments, push-to-talk, run controls, and chat configuration. */
export function Composer({
  draft,
  onDraftChange,
  run,
  disabled,
  commands,
  attachments,
  uploading,
  placeholder,
  focusKey,
  pushToTalkShortcut = true,
  speechToTextEnabled = false,
  executionTargets = [],
  executionTarget = 'host',
  models = [],
  currentModel = '',
  thinkingLevel = '',
  thinkingLevels = [],
  tools = [],
  configDisabled = false,
  executionTargetDisabled = configDisabled,
  toolsDisabled = configDisabled,
  compactDisabled = configDisabled,
  usageTokens = 0,
  usageCost = 0,
  onSelectExecutionTarget = () => undefined,
  onSelectModel = () => undefined,
  onSelectThinking = () => undefined,
  onToolChange = () => undefined,
  onRefreshTools,
  onCompact = () => undefined,
  onAddAttachments,
  onRemoveAttachment,
  onRequestCommands = () => undefined,
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
  placeholder?: string;
  focusKey?: string | null;
  pushToTalkShortcut?: boolean;
  speechToTextEnabled?: boolean;
  executionTargets?: ExecutionTargetOption[];
  executionTarget?: ExecutionTarget;
  models?: ModelOption[];
  currentModel?: string;
  thinkingLevel?: string;
  thinkingLevels?: string[];
  tools?: ToolOption[];
  configDisabled?: boolean;
  executionTargetDisabled?: boolean;
  toolsDisabled?: boolean;
  compactDisabled?: boolean;
  usageTokens?: number;
  usageCost?: number;
  onSelectExecutionTarget?: (target: ExecutionTarget) => void;
  onSelectModel?: (model: string) => void;
  onSelectThinking?: (thinkingLevel: string) => void;
  onToolChange?: (name: string, enabled: boolean) => void;
  onRefreshTools?: () => Promise<void>;
  onCompact?: () => void;
  onAddAttachments: (files: File[]) => void;
  onRemoveAttachment: (id: string) => void;
  onRequestCommands?: () => void;
  onSend: (text: string, mode: SendMode) => void;
  onStop: () => void;
}) {
  const [menuIndex, setMenuIndex] = useState(0);
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  const draftRef = useRef(draft);
  const speechBaseDraft = useRef(draft);
  draftRef.current = draft;
  const busy = run.state !== 'idle';
  const speech = useSpeechToText((text) => {
    const base = speechBaseDraft.current;
    const separator = base && !/\s$/.test(base) ? ' ' : '';
    onDraftChange(`${base}${separator}${text}`);
    inputRef.current?.focus();
  });
  const speechAvailable = speechToTextEnabled && speech.supported;
  const slash = draft.startsWith('/') && !draft.includes(' ') ? draft.slice(1).toLowerCase() : null;
  const matches = slash === null ? [] : commands.filter((command) => command.name.toLowerCase().startsWith(slash)).slice(0, 6);

  const beginSpeech = () => {
    speechBaseDraft.current = draftRef.current;
    void speech.start();
  };

  useEffect(() => {
    if (!speechAvailable || !pushToTalkShortcut || disabled) return;
    const api = window.atelier;
    if (!api) return;
    return api.onPushToTalk((pressed) => {
      if (pressed) {
        speechBaseDraft.current = draftRef.current;
        void speech.start();
      } else {
        speech.stop();
      }
    });
  }, [disabled, pushToTalkShortcut, speech.start, speech.stop, speechAvailable]);

  useEffect(() => {
    if (focusKey && !disabled) inputRef.current?.focus();
  }, [disabled, focusKey]);

  const submit = (alternate: boolean) => {
    const text = draft.trim();
    if ((!text && attachments.length === 0) || disabled || uploading) return;
    onSend(text, busy ? (alternate ? 'followUp' : 'steer') : 'normal');
    inputRef.current?.focus();
  };

  return (
    <div className="composer">
      {matches.length > 0 ? (
        <ul className="slash-menu" role="listbox" aria-label="Commands">
          {matches.map((command, index) => (
            <li key={command.name}>
              <Button type="button" variant="ghost" role="option"
                aria-selected={index === menuIndex} className={`justify-start text-left${index === menuIndex ? ' active' : ''}`}
                onClick={() => onDraftChange(`/${command.name} `)}>
                <strong>/{clip(command.name, 40)}</strong> <span>{clip(command.description, 80)}</span>
              </Button>
            </li>
          ))}
        </ul>
      ) : null}

      {speech.state !== 'idle' ? (
        <div className={`composer-listening-cue ${speech.state}`} role="status" aria-live="polite">
          <span className="composer-listening-dot" aria-hidden="true" />
          {speech.state === 'recording' ? 'Listening — release ⇧ Space to stop' : 'Finishing transcription…'}
        </div>
      ) : null}
      {speech.error ? <p className="composer-speech-error" role="alert">{speech.error}</p> : null}

      <PromptInput
        id="composer-input"
        label="Message"
        value={draft}
        disabled={disabled}
        uploading={uploading}
        running={busy}
        attachments={attachments}
        placeholder={placeholder ?? (busy ? 'Steer the current run, or use Alt+Enter to queue a follow-up…' : 'Ask for a follow-up…')}
        inputRef={inputRef}
        onValueChange={(value) => {
          onDraftChange(value);
          if (value === '/') onRequestCommands();
        }}
        onSubmit={submit}
        onStop={onStop}
        onAddAttachments={onAddAttachments}
        onRemoveAttachment={onRemoveAttachment}
        onKeyDown={(event) => {
          if (matches.length === 0) return;
          if (event.key === 'ArrowDown') {
            event.preventDefault();
            setMenuIndex((index) => (index + 1) % matches.length);
          } else if (event.key === 'ArrowUp') {
            event.preventDefault();
            setMenuIndex((index) => (index - 1 + matches.length) % matches.length);
          } else if (event.key === 'Tab' || (event.key === 'Enter' && !event.shiftKey && !event.altKey)) {
            const picked = matches[menuIndex];
            if (picked) {
              event.preventDefault();
              onDraftChange(`/${picked.name} `);
              setMenuIndex(0);
            }
          } else if (event.key === 'Escape') {
            event.preventDefault();
            onDraftChange('');
          }
        }}
        action={speechAvailable ? (
          <Tooltip>
            <TooltipTrigger render={
              <InputGroupButton type="button" size="icon-sm" variant={speech.state === 'recording' ? 'destructive' : 'secondary'}
                className={`speech-btn${speech.state === 'recording' ? ' recording' : ''}`}
                aria-label={speech.state === 'recording' ? 'Stop listening' : 'Talk'}
                disabled={disabled || speech.state === 'transcribing'}
                onClick={speech.state === 'recording' ? speech.stop : beginSpeech}>
                <Mic aria-hidden="true" />
              </InputGroupButton>
            } />
            <TooltipContent>{speech.state === 'recording' ? 'Stop listening' : 'Talk (hold Shift + Space)'}</TooltipContent>
          </Tooltip>
        ) : null}
        toolbar={
          <>
            {executionTargets.length > 1 ? (
              <Select value={executionTarget} disabled={executionTargetDisabled}
                onValueChange={(value) => onSelectExecutionTarget(value as ExecutionTarget)}>
                <SelectTrigger className="composer-execution-target" aria-label="Execution target">
                  <SelectValue placeholder="Run on…">
                    <ExecutionTargetIcon target={executionTarget} />
                    <span>{executionTargets.find((target) => target.value === executionTarget)?.label ?? executionTarget}</span>
                  </SelectValue>
                </SelectTrigger>
                <SelectContent>
                  {executionTargets.map((target) => (
                    <SelectItem key={target.value} value={target.value}>
                      <ExecutionTargetIcon target={target.value} /> {target.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : null}
            {models.length > 0 || currentModel ? (
              <ModelPicker models={models} current={currentModel} disabled={configDisabled || models.length === 0}
                onSelect={onSelectModel} />
            ) : null}
            {thinkingLevels.length > 0 || thinkingLevel ? (
              <Select value={thinkingLevel || '__unavailable'} disabled={configDisabled || thinkingLevels.length === 0}
                onValueChange={(value) => { if (value) onSelectThinking(value); }}>
                <SelectTrigger className="composer-thinking" aria-label="Thinking budget"><SelectValue placeholder="Thinking" /></SelectTrigger>
                <SelectContent>
                  {thinkingLevel && !thinkingLevels.includes(thinkingLevel) ? <SelectItem value={thinkingLevel}>{thinkingLevel}</SelectItem> : null}
                  {thinkingLevels.map((level) => <SelectItem key={level} value={level}>{level}</SelectItem>)}
                </SelectContent>
              </Select>
            ) : null}
            {tools.length > 0 ? (
              <ToolPicker tools={tools} disabled={toolsDisabled} onChange={onToolChange} onRefresh={onRefreshTools} />
            ) : null}
            {currentModel ? (
              <Tooltip>
                <TooltipTrigger render={
                  <Button type="button" size="sm" variant="ghost" className="compact-btn" onClick={onCompact} disabled={compactDisabled}>
                    <Minimize2 aria-hidden="true" /> Compact
                  </Button>
                } />
                <TooltipContent>Compact conversation history</TooltipContent>
              </Tooltip>
            ) : null}
            {usageTokens > 0 ? <span className="composer-usage">{formatTokens(usageTokens)} · {formatCost(usageCost)}</span> : null}
          </>
        }
      />
    </div>
  );
}
