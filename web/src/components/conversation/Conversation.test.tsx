import { act, fireEvent, render, screen } from '@/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Item, MessageItem } from '@/protocol.gen';
import { createContributionRegistry, removePluginContributions } from '@/plugin-contributions';
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
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('downloads each complete agent message as its original Markdown', () => {
    const createObjectURL = vi.fn((_blob: Blob) => 'blob:message');
    const revokeObjectURL = vi.fn();
    Object.defineProperties(URL, {
      createObjectURL: { value: createObjectURL, configurable: true },
      revokeObjectURL: { value: revokeObjectURL, configurable: true },
    });
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined);

    render(
      <Conversation
        items={[assistantMessage({
          text: '# Result\n\n- one\n- two',
          streaming: false,
          agentName: 'code agent',
          createdAt: '2026-08-07T12:34:56Z',
        })]}
        empty={null}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Download message as Markdown' }));

    expect(createObjectURL).toHaveBeenCalledOnce();
    const blob = createObjectURL.mock.calls[0]?.[0] as Blob;
    expect(blob.type).toBe('text/markdown;charset=utf-8');
    expect(click).toHaveBeenCalledOnce();
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:message');
  });

  it('renders assistant message action slots beside the Markdown download', () => {
    const registry = createContributionRegistry('message-actions-test');
    registry.registerSlot({
      id: 'share',
      slot: 'assistant-message.actions',
      render: context => <button type="button">Share {context.message?.id}</button>,
    });

    const { container } = render(
      <Conversation
        items={[assistantMessage({ id: 'complete', text: 'Done', streaming: false })]}
        contributionContext={{workspace: null, chatId: 'chat_1', session: null}}
        empty={null}
      />,
    );

    const actions = container.querySelector('.msg-actions');
    expect(actions).toContainElement(screen.getByRole('button', {name: 'Share complete'}));
    expect(actions).toContainElement(screen.getByRole('button', {name: 'Download message as Markdown'}));
    act(() => removePluginContributions('message-actions-test'));
  });

  it('only offers downloads for complete agent messages', () => {
    render(
      <Conversation
        items={[
          assistantMessage({ id: 'streaming', text: 'Still working', streaming: true }),
          assistantMessage({ id: 'user', role: 'user', text: 'Question', streaming: false }),
          assistantMessage({ id: 'complete', text: 'Done', streaming: false }),
        ]}
        empty={null}
      />,
    );

    expect(screen.getAllByRole('button', { name: 'Download message as Markdown' })).toHaveLength(1);
  });

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

  it('hides jump to latest and resets scrolling when the conversation is cleared', () => {
    const { container, rerender } = render(
      <Conversation items={[assistantMessage({ text: 'Existing message' })]} empty={<p>Empty</p>} />,
    );
    const conversation = container.querySelector('.conversation') as HTMLDivElement;
    Object.defineProperties(conversation, {
      scrollHeight: { value: 1_000, configurable: true },
      clientHeight: { value: 400, configurable: true },
      scrollTop: { value: 0, writable: true, configurable: true },
    });

    fireEvent.scroll(conversation);
    expect(screen.getByRole('button', { name: 'Jump to latest' })).toBeInTheDocument();

    rerender(<Conversation items={[]} empty={<p>Empty</p>} />);
    expect(screen.queryByRole('button', { name: 'Jump to latest' })).not.toBeInTheDocument();

    rerender(<Conversation items={[assistantMessage({ id: 'new', text: 'New message' })]} empty={<p>Empty</p>} />);
    expect(screen.queryByRole('button', { name: 'Jump to latest' })).not.toBeInTheDocument();
  });

  it('animates rows appended to the active conversation but not initial history', () => {
    const first = assistantMessage({ id: 'first', text: 'Existing' });
    const second = assistantMessage({ id: 'second', text: 'Incoming' });
    const { container, rerender } = render(
      <Conversation items={[first]} contributionContext={{ workspace: null, chatId: 'chat-1', session: null }} empty={null} />,
    );

    expect(container.querySelector('[aria-label="assistant message"]')?.parentElement)
      .not.toHaveClass('conversation-row-enter');

    rerender(
      <Conversation items={[first, second]} contributionContext={{ workspace: null, chatId: 'chat-1', session: null }} empty={null} />,
    );
    const rows = container.querySelectorAll('.conversation-row');
    expect(rows[0]).not.toHaveClass('conversation-row-enter');
    expect(rows[1]).toHaveClass('conversation-row-enter');

    rerender(
      <Conversation items={[first, assistantMessage({ id: 'second', text: 'Incoming update' })]}
        contributionContext={{ workspace: null, chatId: 'chat-1', session: null }} empty={null} />,
    );
    expect(container.querySelectorAll('.conversation-row')[1]).toHaveClass('conversation-row-enter');

    rerender(
      <Conversation items={[assistantMessage({ id: 'other', text: 'Other chat history' })]}
        contributionContext={{ workspace: null, chatId: 'chat-2', session: null }} empty={null} />,
    );
    expect(container.querySelector('.conversation-row')).not.toHaveClass('conversation-row-enter');
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
