import { act, render } from '@/test-utils';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { AutoHeightContainer } from './AutoHeightContainer';

class ResizeObserverMock {
  static instances: ResizeObserverMock[] = [];
  callback: ResizeObserverCallback;
  element: Element | null = null;

  constructor(callback: ResizeObserverCallback) {
    this.callback = callback;
    ResizeObserverMock.instances.push(this);
  }

  observe(element: Element) { this.element = element; }
  disconnect() { this.element = null; }
  unobserve() { this.element = null; }

  resize(width: number, height: number) {
    const entry = { contentRect: { width, height } } as ResizeObserverEntry;
    this.callback([entry], this as unknown as ResizeObserver);
  }
}

describe('AutoHeightContainer', () => {
  it('animates growth after initial layout and snaps shrink', () => {
    vi.useFakeTimers();
    ResizeObserverMock.instances = [];
    vi.stubGlobal('ResizeObserver', ResizeObserverMock);
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
      callback(0);
      return 1;
    });
    vi.stubGlobal('cancelAnimationFrame', vi.fn());

    let addRow: () => void = () => undefined;
    const { container } = render(<Harness register={(add) => { addRow = add; }} />);
    const wrapper = container.querySelector('.conversation-height-transition') as HTMLDivElement;
    const contentObserver = ResizeObserverMock.instances[0];

    act(() => contentObserver?.resize(500, 100));
    expect(wrapper.style.height).toBe('100px');
    expect(wrapper.style.transitionDuration).toBe('');

    act(() => { vi.advanceTimersByTime(250); });
    act(() => { addRow(); });
    act(() => contentObserver?.resize(500, 180));
    expect(wrapper.style.height).toBe('180px');
    expect(wrapper.style.transitionDuration).toBe('');

    act(() => contentObserver?.resize(500, 120));
    expect(wrapper.style.height).toBe('120px');
    expect(wrapper.style.transitionDuration).toBe('');

    vi.useRealTimers();
    vi.unstubAllGlobals();
  });
});

function Harness({ register }: { register: (add: () => void) => void }) {
  const [rows, setRows] = useState(1);
  register(() => setRows((count) => count + 1));
  return (
    <AutoHeightContainer>
      {Array.from({ length: rows }, (_, index) => <div key={index}>Row {index}</div>)}
    </AutoHeightContainer>
  );
}
