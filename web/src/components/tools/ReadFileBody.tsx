import type { ToolActivity } from '@/protocol.gen';
import type { ToolArgs } from './types';
import { languageForPath } from './fileLanguage';
import { FileCode } from './FileCode';
import { number, text } from './utils';

export function ReadFileBody({ tool, args }: { tool: ToolActivity; args: ToolArgs }) {
  const line = number(args, 'line');
  const limit = number(args, 'limit');
  const label = line ? `Contents · lines ${line}${limit ? `–${line + limit - 1}` : '+'}` : 'Contents';
  const language = languageForPath(text(args, 'path'));

  if (!tool.preview) return <p className="tool-empty">No output</p>;

  return (
    <div className="tool-result">
      <div className="tool-result-head">{label}</div>
      {language
        ? <FileCode content={tool.preview} language={language} />
        : <pre className="tool-output" tabIndex={0}>{tool.preview}</pre>}
    </div>
  );
}
