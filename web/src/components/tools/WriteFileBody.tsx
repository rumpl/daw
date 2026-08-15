import type { ToolActivity } from '@/protocol.gen';
import { PlainOutput } from './PlainOutput';
import type { ToolArgs } from './types';
import { formatBytes, number, text } from './utils';

export function WriteFileBody({ tool, args }: { tool: ToolActivity; args: ToolArgs }) {
  const bytes = number(args, 'contentBytes');
  const lines = number(args, 'contentLines');
  const hasContentPreview = typeof args.contentPreview === 'string';
  const content = text(args, 'contentPreview');

  return (
    <div className="mutation-output">
      {bytes !== undefined || lines !== undefined ? (
        <div className="tool-stats">
          {lines !== undefined ? <span><strong>{lines}</strong> lines</span> : null}
          {bytes !== undefined ? <span><strong>{formatBytes(bytes)}</strong></span> : null}
        </div>
      ) : null}
      {hasContentPreview ? (
        <div className="write-preview">
          <div className="tool-result-head">File contents</div>
          <pre className={`tool-output${content ? '' : ' empty-file'}`} tabIndex={0}>{content || '(empty file)'}</pre>
          {args.contentTruncated === true ? <p className="tool-note">File preview truncated for display.</p> : null}
        </div>
      ) : null}
      <PlainOutput tool={tool} label="Result" />
    </div>
  );
}
