import { memo, useEffect, useId, useRef, useState } from 'react';

/**
 * Mermaid renders a ```mermaid fenced code block as an SVG diagram.
 *
 * Security notes:
 *   - Mermaid is initialised with securityLevel 'strict', which runs the
 *     generated SVG through the library's built-in DOMPurify pass and strips
 *     any script/event-handler content. The diagram source is model output,
 *     never raw browser HTML.
 *   - This is the ONLY place in the app that uses dangerouslySetInnerHTML.
 *     It is confined to sanitized, library-generated SVG so the "never
 *     interpret embedded HTML from message text" guarantee still holds:
 *     mermaid re-parses its own restricted grammar rather than passing the
 *     input through as markup.
 *
 * Rendering is async and lazy: the mermaid bundle (large) is only pulled in
 * the first time a diagram appears, keeping the initial payload small.
 */
export const Mermaid = memo(function Mermaid({ code }: { code: string }) {
  const id = useId().replace(/[^a-zA-Z0-9-]/g, '');
  const [svg, setSvg] = useState('');
  const [error, setError] = useState<string | null>(null);
  const containerRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    let cancelled = false;
    setError(null);

    (async () => {
      try {
        const { default: mermaid } = await import('mermaid');
        const dark = window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false;
        mermaid.initialize({
          startOnLoad: false,
          securityLevel: 'strict',
          theme: dark ? 'dark' : 'default',
        });
        // parse throws on invalid syntax before we attempt to render.
        await mermaid.parse(code);
        const { svg: out } = await mermaid.render(`mermaid-${id}`, code);
        if (!cancelled) setSvg(out);
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : 'Failed to render diagram');
          setSvg('');
        }
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [code, id]);

  if (error) {
    return (
      <div className="md-mermaid md-mermaid-error" role="img" aria-label="diagram failed to render">
        <p className="md-mermaid-error-msg">Diagram error: {error}</p>
        <pre className="md-pre">{code}</pre>
      </div>
    );
  }

  if (!svg) {
    return (
      <div className="md-mermaid md-mermaid-loading" aria-busy="true">
        Rendering diagram…
      </div>
    );
  }

  return (
    <div
      ref={containerRef}
      className="md-mermaid"
      role="img"
      aria-label="diagram"
      // Sanitized, mermaid-generated SVG only. See component doc comment.
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
});
