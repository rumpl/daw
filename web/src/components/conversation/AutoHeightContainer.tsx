import { useLayoutEffect, useRef, type ReactNode } from 'react';

const DURATION_MS = 180;
const EASING = 'cubic-bezier(0.16, 1, 0.3, 1)';
const INITIAL_SETTLE_MS = 250;

export function AutoHeightContainer({ children, onHeightChange }: {
  children: ReactNode;
  onHeightChange?: () => void;
}) {
  const wrapperRef = useRef<HTMLDivElement>(null);
  const innerRef = useRef<HTMLDivElement>(null);
  const onHeightChangeRef = useRef(onHeightChange);
  onHeightChangeRef.current = onHeightChange;

  useLayoutEffect(() => {
    const wrapper = wrapperRef.current;
    const inner = innerRef.current;
    if (!wrapper || !inner || typeof ResizeObserver === 'undefined') return;

    wrapper.style.height = `${inner.offsetHeight}px`;
    let settled = false;
    let lastWidth: number | null = null;
    let restoreFrame: number | null = null;
    const settleTimer = window.setTimeout(() => { settled = true; }, INITIAL_SETTLE_MS);

    const snapTo = (height: number) => {
      wrapper.style.transitionDuration = '0s';
      wrapper.style.height = `${height}px`;
      if (restoreFrame !== null) cancelAnimationFrame(restoreFrame);
      restoreFrame = requestAnimationFrame(() => {
        restoreFrame = null;
        wrapper.style.transitionDuration = '';
      });
    };

    const observer = new ResizeObserver(([entry]) => {
      if (!entry) return;
      const { width, height } = entry.contentRect;
      const currentHeight = Number.parseFloat(wrapper.style.height);
      const widthChanged = lastWidth !== null && width !== lastWidth;
      lastWidth = width;

      // Initial layout, text reflow, and disappearing rows should not call
      // attention to themselves. Only stable-width growth gets the glide.
      if (!settled || widthChanged || height < currentHeight) {
        snapTo(height);
      } else {
        wrapper.style.height = `${height}px`;
      }
    });
    observer.observe(inner);
    const wrapperObserver = new ResizeObserver(() => onHeightChangeRef.current?.());
    wrapperObserver.observe(wrapper);

    const syncAfterRestore = () => {
      if (document.visibilityState === 'visible') snapTo(inner.offsetHeight);
    };
    document.addEventListener('visibilitychange', syncAfterRestore);
    window.addEventListener('pageshow', syncAfterRestore);

    return () => {
      observer.disconnect();
      wrapperObserver.disconnect();
      window.clearTimeout(settleTimer);
      if (restoreFrame !== null) cancelAnimationFrame(restoreFrame);
      document.removeEventListener('visibilitychange', syncAfterRestore);
      window.removeEventListener('pageshow', syncAfterRestore);
    };
  }, []);

  return (
    <div
      ref={wrapperRef}
      className="conversation-height-transition"
      style={{ transition: `height ${DURATION_MS}ms ${EASING}` }}
    >
      <div ref={innerRef} className="conversation-height-content">
        {children}
      </div>
    </div>
  );
}
