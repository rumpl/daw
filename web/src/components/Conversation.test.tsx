import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { Item, MessageItem } from '../protocol.gen';
import { Conversation } from './Conversation';

function assistantMessage(overrides: Partial<MessageItem> = {}): Item {
  return {
    kind: 'message',
    message: {
      id: 'message-1',
      role: 'assistant',
      agentName: 'assistant',
      text: '',
      reasoning: '',
      streaming: true,
      createdAt: '2026-08-07T00:00:00Z',
      model: '',
      ...overrides,
    },
  };
}

describe('Conversation', () => {
  it('renders assistant Markdown while the message is streaming', () => {
    const { container } = render(
      <Conversation
        items={[assistantMessage({ text: '# Streaming heading\n\n- first\n- second' })]}
        empty={null}
      />,
    );

    expect(container.querySelector('.msg-streaming h1')).toHaveTextContent('Streaming heading');
    expect(container.querySelectorAll('.msg-streaming li')).toHaveLength(2);
    expect(container.querySelector('.msg-streaming .caret')).not.toBeNull();
    expect(container.querySelector('.msg-streaming pre')).toBeNull();
  });

  it('does not render user messages as Markdown', () => {
    const { container } = render(
      <Conversation
        items={[assistantMessage({ role: 'user', text: '# Plain user text', streaming: false })]}
        empty={null}
      />,
    );

    expect(container.querySelector('.msg-plain')).toHaveTextContent('# Plain user text');
    expect(container.querySelector('.msg h1')).toBeNull();
  });
});
