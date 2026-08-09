import type { DashboardEvent, DashboardEventType, Event, EventType } from './protocol.gen';

type DashboardListener = (event: DashboardEvent) => void;
type ChatListener = (event: Event) => void;

function stream<T extends {type: string}>(
  url: () => string,
  types: Set<string> | null,
  listener: (event: T) => void,
  signal: AbortSignal,
) {
  let source: EventSource | null = null;
  let timer: number | null = null;
  let retries = 0;
  let lastSeq = 0;
  let closed = false;

  const connect = () => {
    if (closed || signal.aborted) return;
    const suffix = lastSeq > 0 ? `${url().includes('?') ? '&' : '?'}lastEventId=${lastSeq}` : '';
    source = new EventSource(url() + suffix);
    source.onopen = () => { retries = 0; };
    source.onmessage = (message: MessageEvent<string>) => {
      try {
        const event = JSON.parse(message.data) as T & {seq?: number};
        if (typeof event.seq === 'number') lastSeq = event.seq;
        if (!types || types.has(event.type)) listener(event);
      } catch {
        // Reconnect or a later authoritative event will restore state.
      }
    };
    source.onerror = () => {
      source?.close();
      source = null;
      if (closed || signal.aborted) return;
      retries += 1;
      timer = window.setTimeout(connect, Math.min(1000 * 2 ** Math.min(retries, 4), 15_000));
    };
  };
  connect();
  const close = () => {
    closed = true;
    source?.close();
    if (timer !== null) window.clearTimeout(timer);
  };
  signal.addEventListener('abort', close, { once: true });
  return close;
}

export function createPluginEvents(signal: AbortSignal) {
  const cleanups = new Set<() => void>();
  const track = (cleanup: () => void) => {
    cleanups.add(cleanup);
    return () => { cleanup(); cleanups.delete(cleanup); };
  };
  return Object.freeze({
    subscribeDashboard(options: {types?: DashboardEventType[]} = {}, listener: DashboardListener) {
      return track(stream<DashboardEvent>(() => '/api/events', options.types ? new Set(options.types) : null, listener, signal));
    },
    subscribePlugin(pluginId: string, options: {types?: string[]} = {}, listener: (event: {type: string; seq: number; data?: unknown}) => void) {
      return track(stream<{type: string; seq: number; data?: unknown}>(
        () => `/api/plugins/${encodeURIComponent(pluginId)}/events`,
        options.types ? new Set(options.types) : null,
        listener,
        signal,
      ));
    },
    subscribeChat(chatId: string, options: {types?: EventType[]} = {}, listener: ChatListener) {
      return track(stream<Event>(
        () => `/api/chats/${encodeURIComponent(chatId)}/events`,
        options.types ? new Set(options.types) : null,
        listener,
        signal,
      ));
    },
    close() {
      for (const cleanup of cleanups) cleanup();
      cleanups.clear();
    },
  });
}
