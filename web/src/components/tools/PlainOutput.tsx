import type { ToolActivity } from '@/protocol.gen';

export function PlainOutput({ tool, label = 'Output' }: { tool: ToolActivity; label?: string }) {
  if (!tool.preview) return <p className="tool-empty">No output</p>;

  return (
    <div className="tool-result">
      <div className="tool-result-head">{label}</div>
      <pre className="tool-output" tabIndex={0}>{tool.preview}</pre>
    </div>
  );
}
