import type { ToolActivity } from '@/protocol.gen';
import type { ToolArgs } from './types';
import { number } from './utils';

type EditPreview = { oldText?: unknown; newText?: unknown; removedLines?: unknown; addedLines?: unknown };

export function EditFileBody({ tool, args }: { tool: ToolActivity; args: ToolArgs }) {
  const edits = Array.isArray(args.edits) ? args.edits as EditPreview[] : [];

  return (
    <div className="edit-output">
      {edits.map((edit, index) => (
        <section className="edit-block" key={index}>
          <header>Change {index + 1}<span>−{String(edit.removedLines ?? 0)} +{String(edit.addedLines ?? 0)} lines</span></header>
          {typeof edit.oldText === 'string' && edit.oldText ? <pre className="diff-remove">{edit.oldText}</pre> : null}
          {typeof edit.newText === 'string' && edit.newText ? <pre className="diff-add">{edit.newText}</pre> : null}
        </section>
      ))}
      {number(args, 'editsTruncated') ? <p className="tool-note">{number(args, 'editsTruncated')} more changes not shown.</p> : null}
      {tool.preview ? <div className="edit-status">{tool.preview}</div> : null}
      {!edits.length && !tool.preview ? <p className="tool-empty">Waiting for result…</p> : null}
    </div>
  );
}
