import type { ToolActivity } from '@/protocol.gen';
import { PlainOutput } from './PlainOutput';

type FileSection = { path: string; content: string };

function splitFileSections(preview: string): FileSection[] {
  const matches = [...preview.matchAll(/^=== (.+) ===$/gm)];
  return matches.map((match, index) => {
    const start = (match.index ?? 0) + match[0].length + 1;
    const end = matches[index + 1]?.index ?? preview.length;
    return { path: match[1] ?? '', content: preview.slice(start, end).replace(/\n\n$/, '') };
  });
}

export function ReadMultipleBody({ tool }: { tool: ToolActivity }) {
  const sections = splitFileSections(tool.preview);
  if (!sections.length) return <PlainOutput tool={tool} label="Files" />;

  return (
    <div className="multi-file-output">
      {sections.map((section, index) => (
        <section key={`${section.path}-${index}`}>
          <header><code>{section.path}</code></header>
          <pre className="tool-output" tabIndex={0}>{section.content}</pre>
        </section>
      ))}
    </div>
  );
}
