import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { NewChatPrompt } from './NewChatPrompt';

function ControlledPrompt({ onSubmit }: { onSubmit: (message: string) => void }) {
  const [message, setMessage] = useState('');
  return (
    <NewChatPrompt
      workspaceLabel="dashboard"
      workspacePath="/code/dashboard"
      message={message}
      busy={false}
      onMessageChange={setMessage}
      onSubmit={onSubmit}
    />
  );
}

describe('NewChatPrompt', () => {
  it('fills a suggestion and leaves it editable before starting', async () => {
    const onSubmit = vi.fn();
    render(<ControlledPrompt onSubmit={onSubmit} />);

    await userEvent.click(screen.getByRole('button', { name: 'Understand this codebase' }));

    const input = screen.getByRole('textbox', { name: 'What would you like to work on?' });
    expect(input).toHaveValue('Give me a tour of this codebase: explain the architecture, key files, and how the pieces fit together.');
    expect(onSubmit).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole('button', { name: /Start chat/ }));
    expect(onSubmit).toHaveBeenCalledWith(expect.stringContaining('tour of this codebase'));
  });
});
