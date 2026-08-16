import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@/test-utils';
import userEvent from '@testing-library/user-event';
import { Composer } from './Composer';
import type { RunStatus } from '@/protocol.gen';

const idle: RunStatus = { state: 'idle', runId: '', queue: { steerDepth: 0, steerCapacity: 8, followUpDepth: 0, followUpCapacity: 8, steer: [], followUps: [] } };
const running: RunStatus = { state: 'running', runId: 'r1', queue: { steerDepth: 1, steerCapacity: 8, followUpDepth: 2, followUpCapacity: 8, steer: [{ id: 's1', text: 'steer' }], followUps: [{ id: 'f1', text: 'one' }, { id: 'f2', text: 'two' }] } };

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
      attachments={[]}
      uploading={false}
      onAddAttachments={vi.fn()}
      onRemoveAttachment={vi.fn()}
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

  it('focuses the message input when a chat becomes active', () => {
    render(
      <Composer
        draft=""
        onDraftChange={vi.fn()}
        run={idle}
        disabled={false}
        focusKey="session-1"
        commands={[]}
        attachments={[]}
        uploading={false}
        onAddAttachments={vi.fn()}
        onRemoveAttachment={vi.fn()}
        onSend={vi.fn()}
        onStop={vi.fn()}
      />,
    );
    expect(screen.getByLabelText('Message')).toHaveFocus();
  });

  it('returns focus to the message input after clicking Send', async () => {
    const user = userEvent.setup();
    setup(idle, 'hello');
    await user.click(screen.getByRole('button', { name: 'Send' }));
    expect(screen.getByLabelText('Message')).toHaveFocus();
  });

  it('always offers an explicit Send button', () => {
    setup(idle, 'hi');
    expect(screen.getByRole('button', { name: 'Send' })).toBeEnabled();
  });

  it('steers on Enter and queues a follow-up on Alt+Enter while running', async () => {
    const user = userEvent.setup();
    const { onSend } = setup(running, 'adjust course');
    const box = screen.getByLabelText('Message');
    await user.click(box);

    await user.keyboard('{Enter}');
    expect(onSend).toHaveBeenLastCalledWith('adjust course', 'steer');

    await user.keyboard('{Alt>}{Enter}{/Alt}');
    expect(onSend).toHaveBeenLastCalledWith('adjust course', 'followUp');
  });

  it('shows only Stop as an action while running', () => {
    setup(running, 'hi');
    expect(screen.queryByRole('button', { name: 'Send' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Stop' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Steer' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Follow-up' })).toBeNull();
    expect(screen.getByText(/1 steer · 2 follow-up queued/)).toBeInTheDocument();
  });

  it('offers slash-command autocomplete', async () => {
    setup(idle, '/comp');
    expect(screen.getByRole('option', { name: /compact/ })).toBeInTheDocument();
  });

  it('uploads files selected from the attachment picker', async () => {
    const user = userEvent.setup();
    const onAddAttachments = vi.fn();
    render(
      <Composer
        draft=""
        onDraftChange={vi.fn()}
        run={idle}
        disabled={false}
        commands={[]}
        attachments={[]}
        uploading={false}
        onAddAttachments={onAddAttachments}
        onRemoveAttachment={vi.fn()}
        onSend={vi.fn()}
        onStop={vi.fn()}
      />,
    );
    await user.click(screen.getByRole('button', { name: 'Attach' }));
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    const file = new File(['hello'], 'notes.txt', { type: 'text/plain' });
    await user.upload(input, file);
    expect(onAddAttachments).toHaveBeenCalledWith([file]);
  });

  it('lets users enable and disable the current agent tools', async () => {
    const user = userEvent.setup();
    const onToolChange = vi.fn();
    render(
      <Composer draft="" onDraftChange={vi.fn()} run={idle} disabled={false} commands={[]}
        attachments={[]} uploading={false}
        tools={[{ name: 'read_file', category: 'filesystem', description: 'Read a file', enabled: true }]}
        onToolChange={onToolChange} onAddAttachments={vi.fn()} onRemoveAttachment={vi.fn()}
        onSend={vi.fn()} onStop={vi.fn()} />,
    );
    await user.click(screen.getByRole('button', { name: 'Tools: 1 of 1 enabled' }));
    const checkbox = screen.getByRole('checkbox', { name: /read_file/ });
    expect(checkbox).toBeChecked();
    await user.click(checkbox);
    expect(onToolChange).toHaveBeenCalledWith('read_file', false);
  });

  it('disables Send for an empty draft', () => {
    setup(idle, '   ');
    expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled();
  });
});
