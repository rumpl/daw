import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { createContributionRegistry, removePluginContributions } from '@/plugin-contributions';
import { SessionSideView } from './SessionSideView';

const context = { workspace: null, chatId: 'chat_1', session: null, sessionId: 'session-1' };

describe('SessionSideView', () => {
  afterEach(() => { act(() => removePluginContributions('side-view-test')); });

  it('shows only the current session view and closes it', () => {
    const registry = createContributionRegistry('side-view-test');
    act(() => registry.openSessionSideView({
      id: 'preview', sessionId: 'session-1', title: 'Preview', render: () => 'preview body',
    }));

    const { rerender } = render(<SessionSideView context={context} />);
    expect(screen.getByRole('complementary', { name: 'Preview' })).toHaveTextContent('preview body');

    rerender(<SessionSideView context={{ ...context, sessionId: 'session-2' }} />);
    expect(screen.queryByRole('complementary')).not.toBeInTheDocument();

    rerender(<SessionSideView context={context} />);
    fireEvent.click(screen.getByRole('button', { name: 'Close side view' }));
    expect(screen.queryByRole('complementary')).not.toBeInTheDocument();
  });

  it('resizes the view from its divider', () => {
    const registry = createContributionRegistry('side-view-test');
    act(() => registry.openSessionSideView({
      id: 'preview', sessionId: 'session-1', title: 'Preview', render: () => 'preview',
    }));
    const { container } = render(<SessionSideView context={context} />);
    container.getBoundingClientRect = () => ({
      left: 0, right: 1000, top: 0, bottom: 700, width: 1000, height: 700, x: 0, y: 0, toJSON: () => ({}),
    });
    fireEvent.pointerDown(screen.getByRole('separator', { name: 'Resize side view' }), { clientX: 480 });
    fireEvent(window, new MouseEvent('pointermove', { clientX: 400 }));
    expect(screen.getByRole('complementary')).toHaveStyle({ width: '600px', flexBasis: '600px' });
    fireEvent.pointerUp(window);
  });

  it('replaces an existing view and an old disposer cannot close the replacement', () => {
    const registry = createContributionRegistry('side-view-test');
    let closeOld: () => void = () => undefined;
    act(() => {
      closeOld = registry.openSessionSideView({
        id: 'old', sessionId: 'session-1', title: 'Old', render: () => 'old',
      });
      registry.openSessionSideView({
        id: 'new', sessionId: 'session-1', title: 'New', render: () => 'new',
      });
      closeOld();
    });

    render(<SessionSideView context={context} />);
    expect(screen.getByRole('complementary', { name: 'New' })).toHaveTextContent('new');
  });
});
