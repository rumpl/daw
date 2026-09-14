import type { ToolActivity } from '@/protocol.gen';
import type { ToolArgs } from './types';
import { text } from './utils';

export function ShellBody({ tool, args }: { tool: ToolActivity; args: ToolArgs }) {
  const command = text(args, 'cmd') || text(args, 'command');

  return (
    <div className="shell-terminal">
      {command ? (
        <section className="shell-command" aria-label="Command">
          <span className="shell-prompt" aria-hidden="true">$</span>
          <pre className="tool-output shell-input" tabIndex={0}>{command}</pre>
        </section>
      ) : null}
      <section className="shell-output-section" aria-label="Output">
        {tool.preview ? <pre className="tool-output shell-output" tabIndex={0}>{tool.preview}</pre> : <p className="tool-empty">Waiting for output…</p>}
      </section>
    </div>
  );
}
