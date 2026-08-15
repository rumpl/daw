import type { ToolActivity } from '@/protocol.gen';
import type { ToolArgs } from './types';
import { number, strings } from './utils';

export function PathsBody({ tool, args, verb }: { tool: ToolActivity; args: ToolArgs; verb: string }) {
  const paths = strings(args, 'paths');

  return (
    <div className="paths-output">
      {paths.length ? <ul>{paths.map((path, index) => <li key={`${path}-${index}`}><span aria-hidden="true">{verb === 'Created' ? '+' : '−'}</span><code>{path}</code></li>)}</ul> : null}
      {number(args, 'pathsTruncated') ? <p className="tool-note">{number(args, 'pathsTruncated')} more paths not shown.</p> : null}
      {tool.preview ? <div className="edit-status">{tool.preview}</div> : null}
      {!paths.length && !tool.preview ? <p className="tool-empty">Waiting for result…</p> : null}
    </div>
  );
}
