import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { Minimize2 } from 'lucide-react';
import { useState } from 'react';
import type { Attachment, CommandInfo, ModelOption, RunStatus, ToolOption } from '@/protocol.gen';
import { clip, formatCost, formatTokens } from '@/safety';
import { ModelPicker } from './ModelPicker';
import { PromptInput } from './PromptInput';
import { ToolPicker } from './ToolPicker';

export type SendMode = 'normal' | 'steer' | 'followUp';

/** A multiline message composer with attachments, run controls, and chat configuration. */
export function Composer({
  draft,
  onDraftChange,
  run,
  disabled,
  commands,
  attachments,
  uploading,
  placeholder,
  models = [],
  currentModel = '',
  thinkingLevel = '',
  thinkingLevels = [],
  tools = [],
  configDisabled = false,
  toolsDisabled = configDisabled,
  compactDisabled = configDisabled,
  usageTokens = 0,
  usageCost = 0,
  onSelectModel = () => undefined,
  onSelectThinking = () => undefined,
  onToolChange = () => undefined,
  onRefreshTools,
  onCompact = () => undefined,
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
  placeholder?: string;
  models?: ModelOption[];
  currentModel?: string;
  thinkingLevel?: string;
  thinkingLevels?: string[];
  tools?: ToolOption[];
  configDisabled?: boolean;
  toolsDisabled?: boolean;
  compactDisabled?: boolean;
  usageTokens?: number;
  usageCost?: number;
  onSelectModel?: (model: string) => void;
  onSelectThinking?: (thinkingLevel: string) => void;
  onToolChange?: (name: string, enabled: boolean) => void;
  onRefreshTools?: () => Promise<void>;
  onCompact?: () => void;
  onAddAttachments: (files: File[]) => void;
  onRemoveAttachment: (id: string) => void;
  onSend: (text: string, mode: SendMode) => void;
  onStop: () => void;
}) {
  const [menuIndex, setMenuIndex] = useState(0);
  const busy = run.state !== 'idle';
  const slash = draft.startsWith('/') && !draft.includes(' ') ? draft.slice(1).toLowerCase() : null;
  const matches = slash === null ? [] : commands.filter((command) => command.name.toLowerCase().startsWith(slash)).slice(0, 6);

  const submit = (alternate: boolean) => {
    const text = draft.trim();
    if ((!text && attachments.length === 0) || disabled || uploading) return;
    onSend(text, busy ? (alternate ? 'followUp' : 'steer') : 'normal');
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

      <PromptInput
        id="composer-input"
        label="Message"
        value={draft}
        disabled={disabled}
        uploading={uploading}
        running={busy}
        attachments={attachments}
        placeholder={placeholder ?? (busy ? 'Steer the current run, or use Alt+Enter to queue a follow-up…' : 'Ask for a follow-up…')}
        onValueChange={onDraftChange}
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
        toolbar={
          <>
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

      {busy && (run.queue.steerDepth > 0 || run.queue.followUpDepth > 0) ? (
        <span className="queue-pill" aria-live="polite">
          {run.queue.steerDepth} steer · {run.queue.followUpDepth} follow-up queued
        </span>
      ) : null}
    </div>
  );
}
