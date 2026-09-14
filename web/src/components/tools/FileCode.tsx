import ReactMarkdown from 'react-markdown';
import rehypeHighlight from 'rehype-highlight';
import { all } from 'lowlight';

interface FileCodeProps {
  content: string;
  language: string;
  className?: string;
}

export function FileCode({ content, language, className = '' }: FileCodeProps) {
  const longestBackticks = Math.max(2, ...Array.from(content.matchAll(/`+/g), (match) => match[0].length));
  const fence = '`'.repeat(longestBackticks + 1);

  return (
    <ReactMarkdown
      rehypePlugins={[[rehypeHighlight, { detect: false, ignoreMissing: true, languages: all }]]}
      components={{
        pre({ children }) {
          return <pre className={`tool-output file-code ${className}`.trim()} tabIndex={0}>{children}</pre>;
        },
      }}
    >
      {`${fence}${language}\n${content}\n${fence}`}
    </ReactMarkdown>
  );
}
