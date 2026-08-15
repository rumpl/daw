import { useEffect, useState } from 'react';
import type { DashboardEvent } from '@/protocol.gen';

export function useDashboardEvents(enabled: boolean) {
  const [sessionsRevision, setSessionsRevision] = useState(0);
  const [pluginsRevision, setPluginsRevision] = useState(0);

  useEffect(() => {
    if (!enabled) return;
    const lastSeqKey = 'daw.dashboardEventSeq';
    let cancelled = false;
    let source: EventSource | null = null;
    let reconnectTimer: number | null = null;
    let flushTimer: number | null = null;
    let retries = 0;
    let sessionsPending = false;
    let pluginsPending = false;

    const schedule = (sessions: boolean, plugins: boolean) => {
      sessionsPending ||= sessions;
      pluginsPending ||= plugins;
      if (flushTimer !== null) return;
      flushTimer = window.setTimeout(() => {
        flushTimer = null;
        if (sessionsPending) setSessionsRevision((value) => value + 1);
        if (pluginsPending) setPluginsRevision((value) => value + 1);
        sessionsPending = false;
        pluginsPending = false;
      }, 75);
    };

    const connect = () => {
      if (cancelled) return;
      const savedSeq = sessionStorage.getItem(lastSeqKey) ?? '';
      const suffix = savedSeq ? `?lastEventId=${encodeURIComponent(savedSeq)}` : '';
      source = new EventSource(`/api/events${suffix}`);
      source.onopen = () => {
        retries = 0;
      };
      source.onmessage = (message: MessageEvent<string>) => {
        try {
          const event = JSON.parse(message.data) as DashboardEvent;
          if (event.seq > 0) sessionStorage.setItem(lastSeqKey, String(event.seq));
          switch (event.type) {
            case 'sessions_changed':
              schedule(true, false);
              break;
            case 'plugins_changed':
              schedule(false, true);
              break;
            case 'snapshot':
            case 'gap':
              schedule(true, true);
              break;
          }
        } catch {
          // A malformed frame is ignored; a later event or reconnect resynchronizes state.
        }
      };
      source.onerror = () => {
        source?.close();
        source = null;
        if (cancelled) return;
        retries += 1;
        const delay = Math.min(1000 * 2 ** Math.min(retries, 4), 15_000);
        reconnectTimer = window.setTimeout(connect, delay);
      };
    };

    connect();
    return () => {
      cancelled = true;
      source?.close();
      if (reconnectTimer !== null) window.clearTimeout(reconnectTimer);
      if (flushTimer !== null) window.clearTimeout(flushTimer);
    };
  }, [enabled]);

  return { sessionsRevision, pluginsRevision };
}
