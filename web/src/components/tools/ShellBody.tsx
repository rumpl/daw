import type { ToolActivity } from '@/protocol.gen';

export function ShellBody({ tool }: { tool: ToolActivity }) {
  return (
    <div className="shell-result">
      {tool.preview ? <pre className="tool-output shell-output" tabIndex={0}>{tool.preview}</pre> : <p className="tool-empty">Waiting for output…</p>}
    </div>
  );
}
