import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Composer } from './Composer';
import type { RunStatus } from '../protocol.gen';

const idle: RunStatus = { state: 'idle', runId: '', queue: { steerDepth: 0, steerCapacity: 8, followUpDepth: 0, followUpCapacity: 8 } };
const running: RunStatus = { state: 'running', runId: 'r1', queue: { steerDepth: 1, steerCapacity: 8, followUpDepth: 2, followUpCapacity: 8 } };

function setup(run: RunStatus, draft = '') {
  const onSend = vi.fn();
  const onStop = vi.fn();
  const onDraftChange = vi.fn();
  render(
    <Composer
      draft={draft}
      onDraftChange={onDraftChange}
      run={run}
      disabled={false}
      commands={[{ name: 'compact', description: 'Compact history', kind: 'command' }]}
      onSend={onSend}
      onStop={onStop}
    />,
  );
  return { onSend, onStop, onDraftChange };
}

describe('Composer', () => {
  it('sends on Enter and inserts a newline on Shift+Enter', async () => {
    const user = userEvent.setup();
    const { onSend, onDraftChange } = setup(idle, 'hello');
    const box = screen.getByLabelText('Message');
    await user.click(box);
    await user.keyboard('{Enter}');
    expect(onSend).toHaveBeenCalledWith('hello', 'normal');

    onSend.mockClear();
    await user.keyboard('{Shift>}{Enter}{/Shift}');
    expect(onSend).not.toHaveBeenCalled();
    expect(onDraftChange).toHaveBeenCalled();
  });

  it('always offers an explicit Send button', () => {
    setup(idle, 'hi');
    expect(screen.getByRole('button', { name: 'Send' })).toBeEnabled();
  });

  it('replaces Send with Stop and shows steer/follow-up while running', () => {
    setup(running, 'hi');
    expect(screen.queryByRole('button', { name: 'Send' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Stop' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Steer' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Follow-up' })).toBeInTheDocument();
    expect(screen.getByText(/1 steer · 2 follow-up queued/)).toBeInTheDocument();
  });

  it('offers slash-command autocomplete', async () => {
    setup(idle, '/comp');
    expect(screen.getByRole('option', { name: /compact/ })).toBeInTheDocument();
  });

  it('disables Send for an empty draft', () => {
    setup(idle, '   ');
    expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled();
  });
});
