// URL policy and untrusted-text helpers.
//
// Markdown is rendered through react-markdown's AST -> React path with raw
// HTML disabled, so there is never an HTML string to sanitize and
// dangerouslySetInnerHTML is never used. Links still need an explicit scheme
// policy, and remote images are disabled by default.

const SAFE_SCHEMES = new Set(['http:', 'https:', 'mailto:']);

/**
 * safeUrl returns the URL when it uses an allowed scheme, and undefined for
 * everything else (javascript:, data:, vbscript:, file:, unknown schemes).
 * Relative URLs are resolved against the current origin and therefore safe.
 */
export function safeUrl(href: string | undefined): string | undefined {
  if (!href) return undefined;
  const trimmed = href.trim();
  if (trimmed === '') return undefined;
  // Reject control characters that could smuggle a scheme past the parser.
  if (/[\u0000-\u001f\u007f]/.test(trimmed)) return undefined;
  let url: URL;
  try {
    url = new URL(trimmed, window.location.origin);
  } catch {
    return undefined;
  }
  if (!SAFE_SCHEMES.has(url.protocol)) return undefined;
  return url.toString();
}

/** isExternal reports whether a safe URL leaves this origin. */
export function isExternal(href: string): boolean {
  try {
    const url = new URL(href, window.location.origin);
    return url.origin !== window.location.origin;
  } catch {
    return false;
  }
}

/**
 * clip bounds any display string. Every non-Markdown field (paths, tool
 * names, model refs, agent names, error text) goes through this before it
 * reaches the DOM as a text node.
 */
export function clip(value: string | undefined | null, max = 400): string {
  if (!value) return '';
  const oneLine = value.replace(/\s+/g, ' ').trim();
  return oneLine.length > max ? `${oneLine.slice(0, max)}…` : oneLine;
}

/** formatCost renders a USD cost with sensible precision. */
export function formatCost(cost: number): string {
  if (!cost) return '$0.00';
  if (cost < 0.01) return `$${cost.toFixed(4)}`;
  return `$${cost.toFixed(2)}`;
}

/** formatTokens renders a compact token count. */
export function formatTokens(n: number): string {
  if (n < 1000) return String(n);
  if (n < 1_000_000) return `${(n / 1000).toFixed(1)}k`;
  return `${(n / 1_000_000).toFixed(1)}M`;
}
