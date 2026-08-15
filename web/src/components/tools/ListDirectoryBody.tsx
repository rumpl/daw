import type { ToolActivity } from '@/protocol.gen';
import { PlainOutput } from './PlainOutput';

export function ListDirectoryBody({ tool }: { tool: ToolActivity }) {
  const entries = tool.preview.split('\n').flatMap((line) => {
    const match = /^(DIR|FILE)\s+(.+)$/.exec(line);
    return match ? [{ directory: match[1] === 'DIR', name: match[2] ?? '' }] : [];
  });
  if (!entries.length) return <PlainOutput tool={tool} label="Directory listing" />;

  return (
    <ul className="directory-list">
      {entries.map((entry, index) => (
        <li key={`${entry.name}-${index}`} className={entry.directory ? 'directory' : 'file'}>
          <span aria-hidden="true">{entry.directory ? '▸' : '·'}</span><code>{entry.name}</code>
        </li>
      ))}
    </ul>
  );
}
