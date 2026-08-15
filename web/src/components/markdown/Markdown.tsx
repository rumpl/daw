import { memo, isValidElement } from 'react';
import type { ReactNode } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import remarkMath from 'remark-math';
import rehypeKatex from 'rehype-katex';
import 'katex/dist/katex.min.css';
import { isExternal, safeUrl } from '@/safety';
import { Mermaid } from './Mermaid';

/**
 * Pull the raw source and language out of react-markdown's <pre> child, which
 * is always a single <code> element for a fenced block. Returns null when the
 * shape does not match (e.g. an indented code block without a language).
 */
function fencedCode(child: ReactNode): { lang: string; code: string } | null {
  if (!isValidElement(child)) return null;
  const props = child.props as { className?: string; children?: ReactNode };
  const match = /language-([\w-]+)/.exec(props.className ?? '');
  if (!match) return null;
  const code = typeof props.children === 'string' ? props.children : String(props.children ?? '');
  return { lang: match[1] ?? '', code: code.replace(/\n$/, '') };
}

/**
 * Markdown renders GFM (plus math and mermaid) for completed assistant
 * messages.
 *
 * Math: `remark-math` + `rehype-katex` turn $inline$ and $$block$$ TeX into
 * KaTeX markup. KaTeX generates its own HTML from a restricted grammar; no raw
 * message HTML is ever interpreted.
 *
 * Mermaid: ```mermaid fences render as SVG via the Mermaid component.
 *
 * Raw HTML is NOT enabled (no rehype-raw), so react-markdown's AST-to-React
 * path never produces an HTML string; dangerouslySetInnerHTML is used only by
 * the Mermaid component on sanitized, library-generated SVG.
 */
export const Markdown = memo(function Markdown({ children }: { children: string }) {
  return (
    <div className="md">
      <ReactMarkdown
        remarkPlugins={[remarkGfm, remarkMath]}
        rehypePlugins={[rehypeKatex]}
        // No rehype-raw / skipHtml default keeps embedded HTML as text.
        components={{
          a({ href, children: content }) {
            const safe = safeUrl(href);
            if (!safe) return <span className="md-blocked-link">{content}</span>;
            const external = isExternal(safe);
            return (
              <a href={safe} {...(external ? { target: '_blank', rel: 'noopener noreferrer' } : {})}>
                {content}
              </a>
            );
          },
          img({ alt }) {
            // Remote images are disabled by default: they leak the reader's
            // presence and the CSP blocks them anyway.
            return <span className="md-blocked-image">[image: {alt ?? 'omitted'}]</span>;
          },
          pre({ children: content }) {
            // A ```mermaid fence renders as a diagram instead of a code block.
            const fenced = fencedCode(content);
            if (fenced?.lang === 'mermaid') {
              return <Mermaid code={fenced.code} />;
            }
            return (
              <pre className="md-pre" tabIndex={0}>
                {content}
              </pre>
            );
          },
          table({ children: content }) {
            return (
              <div className="md-table-scroll" tabIndex={0}>
                <table>{content}</table>
              </div>
            );
          },
        }}
      >
        {children}
      </ReactMarkdown>
    </div>
  );
});
