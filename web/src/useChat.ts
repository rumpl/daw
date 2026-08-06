import { useCallback, useEffect, useRef, useState } from 'react';
import type { Event } from './protocol.gen';
import { api } from './api';
import { applySnapshot, initialChatState, reduce, type ChatState, type ConnectionState } from './reducer';

/**
 * useChat owns one SSE subscription and the reduced chat state.
 *
 * Streaming updates are batched into an animation frame so a fast token
 * stream causes one render per frame rather than one render per token.
 */
export function useChat(chatId: string | null) {
  const [state, setState] = useState<ChatState>(initialChatState);
  const [connection, setConnection] = useState<ConnectionState>('disconnected');
  const pending = useRef<Event[]>([]);
  const frame = useRef<number | null>(null);
  const lastSeq = useRef(0);
  const sourceRef = useRef<EventSource | null>(null);
  const retry = useRef<number>(0);
  const timer = useRef<number | null>(null);

  const flush = useCallback(() => {
    frame.current = null;
    const batch = pending.current;
    if (batch.length === 0) return;
    pending.current = [];
    setState((prev) => {
      let next = prev;
      for (const ev of batch) next = reduce(next, ev);
      lastSeq.current = next.seq;
      return next;
    });
  }, []);

  const push = useCallback(
    (ev: Event) => {
      pending.current.push(ev);
      if (frame.current === null) {
        frame.current = window.requestAnimationFrame(flush);
      }
    },
    [flush],
  );

  useEffect(() => {
    if (!chatId) {
      setState(initialChatState());
      setConnection('disconnected');
      return;
    }
    let cancelled = false;
    lastSeq.current = 0;
    setState(initialChatState());

    const connect = () => {
      if (cancelled) return;
      setConnection(retry.current === 0 ? 'connecting' : 'reconnecting');
      const url =
        lastSeq.current > 0
          ? `/api/chats/${encodeURIComponent(chatId)}/events?lastEventId=${lastSeq.current}`
          : `/api/chats/${encodeURIComponent(chatId)}/events`;
      const es = new EventSource(url);
      sourceRef.current = es;

      es.onopen = () => {
        retry.current = 0;
        setConnection('connected');
      };
      es.onmessage = (msg: MessageEvent<string>) => {
        try {
          push(JSON.parse(msg.data) as Event);
        } catch {
          /* ignore malformed frame */
        }
      };
      es.onerror = () => {
        es.close();
        sourceRef.current = null;
        if (cancelled) return;
        setConnection('reconnecting');
        retry.current += 1;
        const delay = Math.min(1000 * 2 ** Math.min(retry.current, 4), 15000);
        timer.current = window.setTimeout(() => {
          if (retry.current > 6) {
            setConnection('disconnected');
          }
          connect();
        }, delay);
      };
    };

    // Named events are used by the server; EventSource only calls onmessage
    // for unnamed events, so listen generically.
    connect();

    return () => {
      cancelled = true;
      if (timer.current !== null) window.clearTimeout(timer.current);
      if (frame.current !== null) window.cancelAnimationFrame(frame.current);
      frame.current = null;
      pending.current = [];
      sourceRef.current?.close();
      sourceRef.current = null;
      setConnection('disconnected');
    };
  }, [chatId, push]);

  const resnapshot = useCallback(async () => {
    if (!chatId) return;
    const snap = await api.snapshot(chatId);
    lastSeq.current = snap.seq;
    setState(applySnapshot(snap));
  }, [chatId]);

  return { state, connection, resnapshot, setState };
}
