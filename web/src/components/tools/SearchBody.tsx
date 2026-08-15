import type { ToolActivity } from '@/protocol.gen';
import { clip } from '@/safety';
import { PlainOutput } from './PlainOutput';
import type { ToolArgs } from './types';
import { text } from './utils';

type SearchResult = { path: string; line: string; column: string; excerpt: string };

export function SearchBody({ tool, args }: { tool: ToolActivity; args: ToolArgs }) {
  const results: SearchResult[] = tool.preview.split('\n').flatMap((row) => {
    const match = /^(.*):(\d+):(\d+):\s?(.*)$/.exec(row);
    return match ? [{ path: match[1] ?? '', line: match[2] ?? '', column: match[3] ?? '', excerpt: match[4] ?? '' }] : [];
  });

  return (
    <div className="search-output">
      {results.length ? results.map((result, index) => (
        <div className="search-hit" key={`${result.path}-${result.line}-${index}`}>
          <div><code>{result.path}</code><span>:{result.line}:{result.column}</span></div>
          <pre>{result.excerpt}</pre>
        </div>
      )) : <PlainOutput tool={tool} label={text(args, 'query') ? `Matches for “${clip(text(args, 'query'), 80)}”` : 'Matches'} />}
    </div>
  );
}
