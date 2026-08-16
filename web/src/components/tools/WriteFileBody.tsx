import ReactMarkdown from 'react-markdown';
import rehypeHighlight from 'rehype-highlight';
import type { ToolActivity } from '@/protocol.gen';
import { PlainOutput } from './PlainOutput';
import type { ToolArgs } from './types';
import { formatBytes, number, text } from './utils';
import { languageForPath } from './fileLanguage';

function HighlightedFile({ content, language }: { content: string; language: string }) {
  const longestBackticks = Math.max(2, ...Array.from(content.matchAll(/`+/g), (match) => match[0].length));
  const fence = '`'.repeat(longestBackticks + 1);
  return (
    <ReactMarkdown
      rehypePlugins={[[rehypeHighlight, { detect: false, ignoreMissing: true }]]}
      components={{
        pre({ children }) {
          return <pre className="tool-output write-code" tabIndex={0}>{children}</pre>;
        },
      }}
    >
      {`${fence}${language}\n${content}\n${fence}`}
    </ReactMarkdown>
  );
}

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
            ? <HighlightedFile content={content} language={language} />
            : <pre className={`tool-output${content ? '' : ' empty-file'}`} tabIndex={0}>{content || '(empty file)'}</pre>}
          {args.contentTruncated === true ? <p className="tool-note">File preview truncated for display.</p> : null}
        </div>
      ) : null}
      <PlainOutput tool={tool} label="Result" />
    </div>
  );
}
