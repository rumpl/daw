import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Attachment, ToolActivity } from '@/protocol.gen';
import { createContributionRegistry, removePluginContributions } from '@/plugin-contributions';
import { Conversation } from './Conversation';

const tool: ToolActivity = {
  id: 'tool-1', name: 'acme_plan', category: 'mcp', agentName: 'root', argsSummary: '',
  state: 'success', preview: 'plan output', truncated: false, outputBytes: 11, isError: false,
};

const attachment: Attachment = {id: 'a1', name: 'report.xml', mimeType: 'application/junit+xml', size: 10};

describe('plugin renderers', () => {
  it('uses matching tool and attachment renderers', () => {
    const registry = createContributionRegistry('render-test');
    registry.registerToolRenderer({id: 'tool', match: value => value.name === 'acme_plan', render: () => 'custom tool'});
    registry.registerAttachmentRenderer({id: 'attachment', match: value => value.mimeType === 'application/junit+xml', render: () => 'custom attachment'});

    render(<Conversation items={[
      {kind: 'message', message: {id: 'm1', role: 'user', agentName: '', text: 'test', reasoning: '', streaming: false, createdAt: '', model: '', attachments: [attachment]}},
      {kind: 'tool', tool},
    ]} empty={null} />);

    expect(screen.getByText('custom tool')).toBeVisible();
    expect(screen.getByText('custom attachment')).toBeVisible();
    act(() => removePluginContributions('render-test'));
  });

  it('adds a matching tool action beside the trigger without toggling the tool', () => {
    const run = vi.fn();
    const registry = createContributionRegistry('tool-action-test');
    registry.registerToolAction({ id: 'preview', label: 'Preview', match: value => value.id === tool.id, run });
    const { container } = render(<Conversation items={[{kind: 'tool', tool}]} empty={null}
      contributionContext={{ workspace: null, chatId: 'chat_1', session: null, sessionId: 'session-1' }} />);

    const button = screen.getByRole('button', { name: 'Preview' });
    expect(button.closest('.tool-header')).not.toBeNull();
    expect(button.closest('.tool-trigger')).toBeNull();
    fireEvent.click(button);
    expect(run).toHaveBeenCalledWith(tool, expect.objectContaining({ sessionId: 'session-1' }));
    expect(container.querySelector('.tool-trigger')).not.toHaveAttribute('data-panel-open');
    return waitFor(() => expect(button).not.toBeDisabled()).then(() => {
      act(() => removePluginContributions('tool-action-test'));
    });
  });

  it('isolates renderer failures', () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const registry = createContributionRegistry('broken-renderer');
    registry.registerToolRenderer({id: 'tool', match: () => true, render: () => { throw new Error('boom'); }});
    render(<Conversation items={[{kind: 'tool', tool}]} empty={null} />);
    expect(screen.getByText('Plugin renderer failed')).toBeVisible();
    act(() => removePluginContributions('broken-renderer'));
  });
});
