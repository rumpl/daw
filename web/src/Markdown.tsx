import { memo } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { isExternal, safeUrl } from './safety';

/**
 * Markdown renders GFM for completed assistant messages.
 *
 * Raw HTML is NOT enabled (no rehype-raw), so react-markdown's AST-to-React
 * path never produces an HTML string; dangerouslySetInnerHTML is never used
 * anywhere in this app. Links go through an explicit scheme allow-list and
 * remote images are dropped.
 */
export const Markdown = memo(function Markdown({ children }: { children: string }) {
  return (
    <div className="md">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
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
