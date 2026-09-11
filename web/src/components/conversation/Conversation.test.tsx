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

  it('copies each complete textual agent response as its original Markdown', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    });

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

    fireEvent.click(screen.getByRole('button', { name: 'Copy assistant response' }));

    expect(writeText).toHaveBeenCalledWith('# Result\n\n- one\n- two');
    expect(await screen.findByRole('button', { name: 'Assistant response copied' })).toBeInTheDocument();
  });

  it('renders assistant message action slots beside the response copy action', () => {
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
    expect(actions).toContainElement(screen.getByRole('button', {name: 'Copy assistant response'}));
    act(() => removePluginContributions('message-actions-test'));
  });

  it('does not render empty completed assistant message rows', () => {
    const { container } = render(
      <Conversation
        items={[
          assistantMessage({ id: 'streaming', text: 'Still working', streaming: true }),
          assistantMessage({ id: 'user', role: 'user', text: 'Question', streaming: false }),
          assistantMessage({ id: 'tool-only', text: '', streaming: false }),
          assistantMessage({ id: 'whitespace', text: '   ', streaming: false }),
          assistantMessage({ id: 'complete', text: 'Done', streaming: false }),
        ]}
        empty={null}
      />,
    );

    expect(screen.getAllByRole('button', { name: 'Copy assistant response' })).toHaveLength(1);
    expect(container.querySelectorAll('.conversation-row')).toHaveLength(3);
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

  it('fades only text appended to a streaming assistant message', () => {
    const initial = assistantMessage({ id: 'stream', text: 'Hello ', streaming: true });
    const { container, rerender } = render(<Conversation items={[initial]} empty={null} />);
    expect(container.querySelector('[class^="stream-token-enter-"]')).toBeNull();

    rerender(<Conversation items={[assistantMessage({ id: 'stream', text: 'Hello world', streaming: true })]} empty={null} />);
    expect(container.querySelector('.stream-token-enter-a')).toHaveTextContent('world');

    rerender(<Conversation items={[assistantMessage({ id: 'stream', text: 'Hello world again', streaming: true })]} empty={null} />);
    expect(container.querySelector('.stream-token-enter-b')?.textContent).toBe(' again');

    rerender(<Conversation items={[assistantMessage({ id: 'stream', text: 'Hello world again', streaming: false })]} empty={null} />);
    expect(container.querySelector('[class^="stream-token-enter-"]')).toBeNull();
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

    const pendingQueue = container.querySelector('.pending-queue');
    expect(pendingQueue).toHaveTextContent('Steerchange direction');
    expect(pendingQueue).toHaveTextContent('Follow-upthen run tests');
    expect(pendingQueue?.closest('.conversation')).toBeNull();
    expect(pendingQueue?.parentElement).toHaveClass('conversation-wrap');
  });

  it('renders image attachments in user messages and opens them full size', () => {
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

    fireEvent.click(screen.getByRole('button', { name: 'View screen.png full size' }));
    const dialog = screen.getByRole('dialog', { name: 'screen.png full size' });
    expect(dialog).toBeVisible();
    expect(dialog.querySelector('img')).toHaveAttribute('src', 'data:image/png;base64,YWJj');
  });

  it('magnifies message markers based on pointer proximity and hides them on leave', () => {
    const user = assistantMessage({ id: 'user-1', role: 'user', text: 'Question', streaming: false });
    const { container } = render(<Conversation items={[user]} empty={null} />);
    const wrap = container.querySelector('.conversation-scroll') as HTMLDivElement;
    const marker = screen.getByRole('button', { name: 'Jump to user message 1' });
    vi.spyOn(wrap, 'getBoundingClientRect').mockReturnValue({
      left: 0, top: 0, right: 500, bottom: 500, width: 500, height: 500, x: 0, y: 0, toJSON: () => ({}),
    });
    vi.spyOn(marker, 'getBoundingClientRect').mockReturnValue({
      left: 470, top: 240, right: 490, bottom: 250, width: 20, height: 10, x: 470, y: 240, toJSON: () => ({}),
    });

    const pointerMove = new Event('pointermove', { bubbles: true });
    Object.defineProperties(pointerMove, {
      clientX: { value: 495 },
      clientY: { value: 245 },
    });
    fireEvent(wrap, pointerMove);
    expect(marker.style.getPropertyValue('--marker-opacity')).not.toBe('0');
    expect(Number(marker.style.getPropertyValue('--marker-scale'))).toBeGreaterThan(2);

    fireEvent.pointerLeave(wrap);
    expect(marker.style.getPropertyValue('--marker-opacity')).toBe('');
    expect(marker.style.getPropertyValue('--marker-scale')).toBe('');
  });

  it('shows one navigation marker per user message and scrolls to it', () => {
    const scrollIntoView = vi.fn();
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
      value: scrollIntoView,
      configurable: true,
    });
    const user = assistantMessage({ id: 'user-1', role: 'user', text: 'First question', streaming: false });
    const assistant = assistantMessage({ id: 'assistant-1', text: 'First answer', streaming: false });
    const secondUser = assistantMessage({ id: 'user-2', role: 'user', text: 'Second question', streaming: false });

    const { container } = render(
      <Conversation items={[user, assistant, secondUser]} empty={null} />,
    );

    expect(screen.getAllByRole('button', { name: /Jump to user message/ })).toHaveLength(2);
    fireEvent.click(screen.getByRole('button', { name: 'Jump to user message 2' }));

    expect(scrollIntoView).toHaveBeenCalledWith({ behavior: 'smooth', block: 'start' });
    expect(container.querySelector('[data-user-message-index="1"] article'))
      .toHaveAttribute('aria-label', 'user message');
  });

  it('renders only the latest history batch and loads earlier items without animating them', () => {
    const items = Array.from({ length: 85 }, (_, index) => assistantMessage({
      id: `message-${index}`,
      text: `Message ${index}`,
      streaming: false,
    }));
    const { container } = render(<Conversation items={items} empty={null} />);

    expect(container.querySelectorAll('.conversation-row')).toHaveLength(80);
    expect(screen.queryByText('Message 0')).not.toBeInTheDocument();
    expect(screen.getByText('Message 84')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Load earlier messages' }));

    expect(container.querySelectorAll('.conversation-row')).toHaveLength(85);
    expect(screen.getByText('Message 0')).toBeInTheDocument();
    expect(container.querySelector('.conversation-row-enter')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Load earlier messages' })).not.toBeInTheDocument();
  });

  it('keeps the latest history window mounted as new items arrive', () => {
    const items = Array.from({ length: 80 }, (_, index) => assistantMessage({
      id: `message-${index}`,
      text: `Message ${index}`,
      streaming: false,
    }));
    const { container, rerender } = render(<Conversation items={items} empty={null} />);

    rerender(<Conversation items={[...items, assistantMessage({ id: 'new', text: 'Newest', streaming: false })]} empty={null} />);

    expect(container.querySelectorAll('.conversation-row')).toHaveLength(80);
    expect(screen.queryByText('Message 0')).not.toBeInTheDocument();
    expect(screen.getByText('Newest')).toBeInTheDocument();
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

  it('renders user messages as Markdown and can toggle their source', () => {
    const { container } = render(
      <Conversation
        items={[assistantMessage({ role: 'user', text: '# User heading\n\n- first\n- second', streaming: false })]}
        empty={null}
      />,
    );

    expect(container.querySelector('.msg h1')).toHaveTextContent('User heading');
    expect(container.querySelectorAll('.msg li')).toHaveLength(2);
    expect(container.querySelector('.msg-plain')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'View user message source' }));
    expect(container.querySelector('.msg-plain')).toHaveTextContent('# User heading');
    expect(container.querySelector('.msg h1')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Render user message as Markdown' }));
    expect(container.querySelector('.msg h1')).toHaveTextContent('User heading');
    expect(container.querySelector('.msg-plain')).toBeNull();
  });
});
