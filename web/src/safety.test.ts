import { describe, expect, it } from 'vitest';
import { clip, formatCost, formatTokens, isExternal, safeUrl } from './safety';

describe('URL policy', () => {
  it('allows only http, https and mailto', () => {
    expect(safeUrl('https://example.com/a')).toBe('https://example.com/a');
    expect(safeUrl('http://example.com/')).toBe('http://example.com/');
    expect(safeUrl('mailto:a@b.c')).toBe('mailto:a@b.c');
  });

  it('rejects executable and unknown schemes', () => {
    const hostile = [
      'javascript:alert(1)',
      'JaVaScRiPt:alert(1)',
      '  javascript:alert(1)  ',
      'data:text/html;base64,PHNjcmlwdD4=',
      'vbscript:msgbox',
      'file:///etc/passwd',
      'chrome://settings',
      'weird-scheme://x',
      'java\nscript:alert(1)',
      'java\u0000script:alert(1)',
    ];
    for (const h of hostile) {
      expect(safeUrl(h), h).toBeUndefined();
    }
  });

  it('treats relative links as same-origin', () => {
    const url = safeUrl('/local/page');
    expect(url).toBeDefined();
    expect(isExternal(url as string)).toBe(false);
  });

  it('detects external links', () => {
    expect(isExternal('https://example.com/x')).toBe(true);
  });

  it('ignores empty input', () => {
    expect(safeUrl(undefined)).toBeUndefined();
    expect(safeUrl('   ')).toBeUndefined();
  });
});

describe('display bounding', () => {
  it('clips long untrusted strings and flattens newlines', () => {
    const out = clip('a\nb'.repeat(500), 40);
    expect(out.length).toBeLessThanOrEqual(41);
    expect(out).not.toContain('\n');
  });

  it('handles nullish values', () => {
    expect(clip(undefined)).toBe('');
    expect(clip(null)).toBe('');
  });
});

describe('formatting', () => {
  it('formats cost and tokens compactly', () => {
    expect(formatCost(0)).toBe('$0.00');
    expect(formatCost(0.0004)).toBe('$0.0004');
    expect(formatCost(12.5)).toBe('$12.50');
    expect(formatTokens(999)).toBe('999');
    expect(formatTokens(1500)).toBe('1.5k');
    expect(formatTokens(2_500_000)).toBe('2.5M');
  });
});
