import type { ToolActivity } from '@/protocol.gen';
import { PlainOutput } from './PlainOutput';
import type { ToolArgs } from './types';
import { FileCode } from './FileCode';
import { formatBytes, number, text } from './utils';
import { languageForPath } from './fileLanguage';

export function WriteFileBody({ tool, args }: { tool: ToolActivity; args: ToolArgs }) {
  const bytes = number(args, 'contentBytes');
  const lines = number(args, 'contentLines');
  const hasContentPreview = typeof args.contentPreview === 'string';
  const content = text(args, 'contentPreview');
  const language = languageForPath(text(args, 'path'));

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
          {content && language
            ? <FileCode content={content} language={language} className="write-code" />
            : <pre className={`tool-output${content ? '' : ' empty-file'}`} tabIndex={0}>{content || '(empty file)'}</pre>}
          {args.contentTruncated === true ? <p className="tool-note">File preview truncated for display.</p> : null}
        </div>
      ) : null}
      <PlainOutput tool={tool} label="Result" />
    </div>
  );
}
