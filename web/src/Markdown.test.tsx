import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Markdown } from './Markdown';

describe('Markdown rendering', () => {
  it('renders GFM tables and code without raw HTML', () => {
    const { container } = render(
      <Markdown>{'| a | b |\n| - | - |\n| 1 | 2 |\n\n```sh\nls -la\n```'}</Markdown>,
    );
    expect(container.querySelector('table')).not.toBeNull();
    expect(container.querySelector('pre')).not.toBeNull();
  });

  it('never interprets embedded HTML', () => {
    const { container } = render(<Markdown>{'<img src=x onerror="alert(1)"> <script>alert(2)</script>'}</Markdown>);
    expect(container.querySelector('script')).toBeNull();
    expect(container.querySelector('img')).toBeNull();
    expect(container.textContent).toContain('<script>');
  });

  it('blocks javascript: links but keeps their text', () => {
    const { container } = render(<Markdown>{'[click me](javascript:alert(1))'}</Markdown>);
    expect(container.querySelector('a')).toBeNull();
    expect(screen.getByText('click me')).toBeInTheDocument();
  });

  it('blocks data: URLs', () => {
    const { container } = render(<Markdown>{'[x](data:text/html;base64,PHN2Zz4=)'}</Markdown>);
    expect(container.querySelector('a')).toBeNull();
  });

  it('marks external links noopener noreferrer', () => {
    const { container } = render(<Markdown>{'[docs](https://example.com/docs)'}</Markdown>);
    const a = container.querySelector('a');
    expect(a?.getAttribute('href')).toBe('https://example.com/docs');
    expect(a?.getAttribute('rel')).toBe('noopener noreferrer');
    expect(a?.getAttribute('target')).toBe('_blank');
  });

  it('disables remote images', () => {
    const { container } = render(<Markdown>{'![alt](https://example.com/tracker.png)'}</Markdown>);
    expect(container.querySelector('img')).toBeNull();
    expect(container.textContent).toContain('[image: alt]');
  });
});
