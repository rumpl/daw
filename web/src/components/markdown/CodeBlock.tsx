import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { Check, Copy } from 'lucide-react';

const languageNames: Record<string, string> = {
  bash: 'Bash', c: 'C', cpp: 'C++', csharp: 'C#', css: 'CSS', go: 'Go', html: 'HTML',
  java: 'Java', javascript: 'JavaScript', js: 'JavaScript', json: 'JSON', jsx: 'JSX',
  markdown: 'Markdown', md: 'Markdown', python: 'Python', py: 'Python', ruby: 'Ruby',
  rust: 'Rust', shell: 'Shell', sh: 'Shell', sql: 'SQL', ts: 'TypeScript', tsx: 'TSX',
  typescript: 'TypeScript', xml: 'XML', yaml: 'YAML', yml: 'YAML',
};

export function CodeBlock({ code, language, children }: { code: string; language: string; children: ReactNode }) {
  const [copied, setCopied] = useState(false);
  const resetTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const normalizedLanguage = language.toLowerCase();

  useEffect(() => () => clearTimeout(resetTimer.current), []);

  async function copyCode() {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      clearTimeout(resetTimer.current);
      resetTimer.current = setTimeout(() => setCopied(false), 2000);
    } catch {
      setCopied(false);
    }
  }

  return (
    <figure className="md-code-block">
      <figcaption className="md-code-header">
        <span>{languageNames[normalizedLanguage] ?? language}</span>
        <button type="button" onClick={copyCode} aria-label={copied ? 'Code copied' : 'Copy code'}>
          {copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
          <span>{copied ? 'Copied' : 'Copy'}</span>
        </button>
      </figcaption>
      <pre className="md-pre" tabIndex={0}>{children}</pre>
    </figure>
  );
}
