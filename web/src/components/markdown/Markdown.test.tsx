import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Markdown } from './Markdown';

describe('Markdown rendering', () => {
  it('wraps only the newly appended source range for streaming animation', () => {
    const { container, rerender } = render(
      <Markdown animateFrom={6} animationPhase="a">Hello new tokens</Markdown>,
    );
    const animated = container.querySelector('.stream-token-enter-a');
    expect(animated).toHaveTextContent('new tokens');
    expect(container.querySelector('p')?.childNodes[0]?.textContent).toBe('Hello ');

    rerender(<Markdown animateFrom={7} animationPhase="b">**Bold new** tail</Markdown>);
    expect(container.querySelector('.stream-token-enter-b')?.textContent).toBe('new');
    expect(container.querySelector('strong')).toHaveTextContent('Bold new');
  });

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

  it('renders inline LaTeX math via KaTeX', () => {
    const { container } = render(<Markdown>{'Euler: $e^{i\\pi} + 1 = 0$'}</Markdown>);
    expect(container.querySelector('.katex')).not.toBeNull();
  });

  it('renders block LaTeX math via KaTeX', () => {
    const { container } = render(<Markdown>{'$$\n\\int_0^1 x^2\\,dx = \\frac{1}{3}\n$$'}</Markdown>);
    expect(container.querySelector('.katex-display')).not.toBeNull();
  });

  it('routes a mermaid fence to the diagram renderer, not a code block', () => {
    const { container } = render(<Markdown>{'```mermaid\ngraph TD; A-->B;\n```'}</Markdown>);
    // The mermaid fence must not fall through to the plain code-block path.
    expect(container.querySelector('pre.md-pre')).toBeNull();
    expect(container.querySelector('.md-mermaid')).not.toBeNull();
  });

  it('syntax-highlights a fenced language in a labeled code card', () => {
    const { container } = render(<Markdown>{'```go\npackage main\n\nfunc main() {}\n```'}</Markdown>);
    expect(container.querySelector('.md-code-block')).not.toBeNull();
    expect(container.querySelector('.language-go .hljs-keyword')?.textContent).toBe('package');
    expect(screen.getByText('Go')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Copy code' })).toBeInTheDocument();
  });

  it('falls back to unhighlighted code for an unknown language', () => {
    const { container } = render(<Markdown>{'```madeup\nhello world\n```'}</Markdown>);
    expect(container.querySelector('.language-madeup')?.textContent).toContain('hello world');
  });

  it('keeps a non-mermaid code fence as a code block', () => {
    const { container } = render(<Markdown>{'```js\nconst a = 1;\n```'}</Markdown>);
    expect(container.querySelector('pre.md-pre')).not.toBeNull();
    expect(container.querySelector('.md-code-block')).not.toBeNull();
    expect(container.querySelector('.md-mermaid')).toBeNull();
  });
});
