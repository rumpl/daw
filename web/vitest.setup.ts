import '@testing-library/jest-dom/vitest';

class ResizeObserverMock implements ResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

Object.defineProperty(globalThis, 'ResizeObserver', {
  configurable: true,
  value: ResizeObserverMock,
});

Object.defineProperty(Element.prototype, 'getAnimations', {
  configurable: true,
  value: () => [],
});
