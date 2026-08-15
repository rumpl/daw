import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupTextarea,
} from '@/components/ui/input-group';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import type { Attachment } from '@/protocol.gen';
import { clip } from '@/safety';
import { ArrowUp, Paperclip, Square, X } from 'lucide-react';
import { useEffect, useRef, type KeyboardEvent, type ReactNode, type RefObject } from 'react';

const ACCEPTED_FILES = 'image/jpeg,image/png,image/gif,image/webp,application/pdf,text/*,.txt,.json,.yaml,.yml,.toml,.go,.js,.jsx,.ts,.tsx,.py,.rs,.java,.log,.md';

interface PromptInputProps {
  id: string;
  label: string;
  value: string;
  placeholder: string;
  disabled: boolean;
  uploading?: boolean;
  running?: boolean;
  attachments?: Attachment[];
  toolbar?: ReactNode;
  inputRef?: RefObject<HTMLTextAreaElement | null>;
  submitLabel?: string;
  onValueChange: (value: string) => void;
  onSubmit: (alternate: boolean) => void;
  onStop?: () => void;
  onAddAttachments?: (files: File[]) => void;
  onRemoveAttachment?: (id: string) => void;
  onKeyDown?: (event: KeyboardEvent<HTMLTextAreaElement>) => void;
}

/** Shared AI prompt input built from shadcn's InputGroup primitives. */
export function PromptInput({
  id,
  label,
  value,
  placeholder,
  disabled,
  uploading = false,
  running = false,
  attachments = [],
  toolbar,
  inputRef,
  submitLabel = 'Send',
  onValueChange,
  onSubmit,
  onStop,
  onAddAttachments,
  onRemoveAttachment,
  onKeyDown,
}: PromptInputProps) {
  const localTextarea = useRef<HTMLTextAreaElement | null>(null);
  const textarea = inputRef ?? localTextarea;
  const fileInput = useRef<HTMLInputElement | null>(null);
  const cannotSubmit = disabled || uploading || (!value.trim() && attachments.length === 0);

  useEffect(() => {
    const element = textarea.current;
    if (!element) return;
    if (!value) {
      element.style.height = '';
      return;
    }
    element.style.height = 'auto';
    element.style.height = `${Math.min(element.scrollHeight, 220)}px`;
  }, [textarea, value]);

  return (
    <form className="prompt-input" onSubmit={(event) => {
      event.preventDefault();
      if (!cannotSubmit) onSubmit(false);
    }}>
      <InputGroup className="composer-card">
        {attachments.length > 0 ? (
          <InputGroupAddon align="block-start" className="prompt-input-attachments">
            <ul className="composer-attachments" aria-label="Attachments">
              {attachments.map((attachment) => (
                <li key={attachment.id}>
                  <span>{clip(attachment.name, 60)}</span>
                  <small>{Math.ceil(attachment.size / 1024)} KB</small>
                  <InputGroupButton type="button" size="icon-xs" variant="ghost" aria-label={`Remove ${attachment.name}`}
                    onClick={() => onRemoveAttachment?.(attachment.id)}><X aria-hidden="true" /></InputGroupButton>
                </li>
              ))}
            </ul>
          </InputGroupAddon>
        ) : null}

        <label className="sr-only" htmlFor={id}>{label}</label>
        <InputGroupTextarea id={id} ref={textarea} value={value} rows={2} disabled={disabled}
          placeholder={placeholder} onChange={(event) => onValueChange(event.target.value)}
          onKeyDown={(event) => {
            onKeyDown?.(event);
            if (event.defaultPrevented || event.key !== 'Enter' || event.shiftKey || event.nativeEvent.isComposing) return;
            event.preventDefault();
            if (!cannotSubmit) onSubmit(event.altKey);
          }}
          onPaste={(event) => {
            const files = Array.from(event.clipboardData.files);
            if (files.length > 0) onAddAttachments?.(files);
          }} />

        {onAddAttachments ? (
          <input ref={fileInput} className="sr-only" type="file" multiple accept={ACCEPTED_FILES}
            onChange={(event) => {
              onAddAttachments(Array.from(event.target.files ?? []));
              event.target.value = '';
            }} />
        ) : null}

        <InputGroupAddon align="block-end" className="composer-toolbar">
          <div className="composer-config">{toolbar}</div>
          <div className="composer-actions">
            {onAddAttachments ? (
              <Tooltip>
                <TooltipTrigger render={
                  <InputGroupButton type="button" size="icon-sm" variant="ghost" className="attach-btn" aria-label="Attach"
                    disabled={disabled || uploading || attachments.length >= 4} onClick={() => fileInput.current?.click()}>
                    <Paperclip aria-hidden="true" />
                  </InputGroupButton>
                } />
                <TooltipContent>{uploading ? 'Adding attachments…' : 'Attach files'}</TooltipContent>
              </Tooltip>
            ) : null}
            {running && onStop ? (
              <Tooltip>
                <TooltipTrigger render={
                  <InputGroupButton type="button" size="icon-sm" variant="destructive" className="stop-btn" aria-label="Stop" onClick={onStop}>
                    <Square fill="currentColor" aria-hidden="true" />
                  </InputGroupButton>
                } />
                <TooltipContent>Stop</TooltipContent>
              </Tooltip>
            ) : (
              <Tooltip>
                <TooltipTrigger render={
                  <InputGroupButton type="submit" size="icon-sm" className="send-btn rounded-full"
                    aria-label={submitLabel} disabled={cannotSubmit}>
                    <ArrowUp aria-hidden="true" />
                  </InputGroupButton>
                } />
                <TooltipContent>{submitLabel}</TooltipContent>
              </Tooltip>
            )}
          </div>
        </InputGroupAddon>
      </InputGroup>
    </form>
  );
}
