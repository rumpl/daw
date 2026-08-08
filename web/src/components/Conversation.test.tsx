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

  it('renders reasoning with its existing styling class and Markdown', () => {
    const { container } = render(
      <Conversation
        items={[assistantMessage({ reasoning: '**Considering** the options:\n\n- first\n- second' })]}
        empty={null}
      />,
    );

    const reasoning = container.querySelector('.reasoning');
    expect(reasoning).not.toBeNull();
    expect(reasoning?.querySelector('strong')).toHaveTextContent('Considering');
    expect(reasoning?.querySelectorAll('li')).toHaveLength(2);
  });

  it('does not render agent or model metadata in messages', () => {
    const { container } = render(
      <Conversation
        items={[assistantMessage({ agentName: 'ROOT', model: 'example/model', text: 'Response' })]}
        empty={null}
      />,
    );

    expect(container.querySelector('.msg-head')).toBeNull();
    expect(container.querySelector('.msg')).not.toHaveTextContent('ROOT');
    expect(container.querySelector('.msg')).not.toHaveTextContent('example/model');
  });

  it('renders pending steer and follow-up messages', () => {
    const { container } = render(
      <Conversation
        items={[assistantMessage({ text: 'Working' })]}
        queue={{
          steerDepth: 1,
          steerCapacity: 5,
          followUpDepth: 1,
          followUpCapacity: 20,
          steer: [{ id: 's1', text: 'change direction' }],
          followUps: [{ id: 'f1', text: 'then run tests' }],
        }}
        empty={null}
      />,
    );

    expect(container.querySelector('.pending-queue')).toHaveTextContent('Steerchange direction');
    expect(container.querySelector('.pending-queue')).toHaveTextContent('Follow-upthen run tests');
  });

  it('renders image attachments in user messages', () => {
    const { container } = render(
      <Conversation
        items={[assistantMessage({
          role: 'user',
          text: 'What is this?',
          streaming: false,
          attachments: [{
            id: 'image-1', name: 'screen.png', mimeType: 'image/png', size: 3, data: 'YWJj',
          }],
        })]}
        empty={null}
      />,
    );

    const image = container.querySelector('.message-attachment-image img') as HTMLImageElement;
    expect(image).not.toBeNull();
    expect(image.src).toBe('data:image/png;base64,YWJj');
    expect(image).toHaveAttribute('alt', 'screen.png');
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
