import { render, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Mermaid } from './Mermaid';

const mermaid = vi.hoisted(() => ({
  initialize: vi.fn(),
  parse: vi.fn<(code: string) => Promise<void>>(),
  render: vi.fn<(id: string, code: string) => Promise<{ svg: string }>>(),
}));

vi.mock('mermaid', () => ({ default: mermaid }));

describe('Mermaid', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mermaid.parse.mockImplementation(async (code) => {
      if (code === 'invalid') throw new Error('invalid syntax');
    });
    mermaid.render.mockImplementation(async (_id, code) => ({ svg: `<svg data-code="${code}"></svg>` }));
  });

  it('waits for streaming source to settle before replacing a valid diagram', async () => {
    const { container, rerender } = render(<Mermaid code="graph TD; A-->B" streaming={false} />);
    await waitFor(() => expect(container.querySelector('svg')).toHaveAttribute('data-code', 'graph TD; A-->B'));

    rerender(<Mermaid code="graph TD; A-->B; B-->C" streaming />);
    await new Promise((resolve) => window.setTimeout(resolve, 50));
    expect(container.querySelector('svg')).toHaveAttribute('data-code', 'graph TD; A-->B');

    await waitFor(() => expect(container.querySelector('svg')).toHaveAttribute('data-code', 'graph TD; A-->B; B-->C'));
  });

  it('keeps the last valid diagram while streaming invalid syntax', async () => {
    const { container, rerender } = render(<Mermaid code="graph TD; A-->B" streaming />);
    await waitFor(() => expect(container.querySelector('svg')).toHaveAttribute('data-code', 'graph TD; A-->B'));

    rerender(<Mermaid code="invalid" streaming />);
    await waitFor(() => expect(mermaid.parse).toHaveBeenCalledWith('invalid'));

    expect(container.querySelector('svg')).toHaveAttribute('data-code', 'graph TD; A-->B');
    expect(container.querySelector('.md-mermaid-error')).toBeNull();
  });

  it('shows invalid syntax once streaming has finished', async () => {
    const { container, rerender } = render(<Mermaid code="graph TD; A-->B" streaming />);
    await waitFor(() => expect(container.querySelector('svg')).not.toBeNull());

    rerender(<Mermaid code="invalid" streaming={false} />);

    await waitFor(() => expect(container.querySelector('.md-mermaid-error')).not.toBeNull());
    expect(container.querySelector('svg')).toBeNull();
  });
});
