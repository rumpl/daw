import { useCallback, useEffect, useRef, useState } from 'react';
import type { Event } from './protocol.gen';
import { api } from './api';
import { applySnapshot, initialChatState, reduce, type ChatState, type ConnectionState } from './reducer';

interface CachedChatState {
  chatId: string | null;
  state: ChatState;
}

/**
 * useChat owns one SSE subscription and retains the reduced state of chats
 * visited during this dashboard session. Retaining state avoids flashing an
 * empty conversation while a previously opened tab reconnects.
 *
 * Streaming updates are batched into an animation frame so a fast token
 * stream causes one render per frame rather than one render per token.
 */
export function useChat(chatId: string | null) {
  const cache = useRef(new Map<string, ChatState>());
  const activeChatId = useRef(chatId);
  activeChatId.current = chatId;
  const [cachedState, setCachedState] = useState<CachedChatState>({
    chatId,
    state: initialChatState(),
  });
  const state = cachedState.chatId === chatId
    ? cachedState.state
    : (chatId ? cache.current.get(chatId) : undefined) ?? initialChatState();
  const [connection, setConnection] = useState<ConnectionState>('disconnected');
  const pending = useRef<Array<{ chatId: string; event: Event }>>([]);
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
    setCachedState((current) => {
      const nextByChat = new Map<string, ChatState>();
      for (const { chatId: eventChatId, event } of batch) {
        const previous = nextByChat.get(eventChatId)
          ?? (current.chatId === eventChatId ? current.state : cache.current.get(eventChatId))
          ?? initialChatState();
        nextByChat.set(eventChatId, reduce(previous, event));
      }
      for (const [eventChatId, next] of nextByChat) cache.current.set(eventChatId, next);

      const active = activeChatId.current;
      const nextActive = active ? nextByChat.get(active) : undefined;
      if (active && nextActive) {
        lastSeq.current = nextActive.seq;
        return { chatId: active, state: nextActive };
      }
      return current;
    });
  }, []);

  const push = useCallback(
    (eventChatId: string, event: Event) => {
      pending.current.push({ chatId: eventChatId, event });
      if (frame.current === null) {
        frame.current = window.requestAnimationFrame(flush);
      }
    },
    [flush],
  );

  useEffect(() => {
    if (!chatId) {
      setCachedState({ chatId: null, state: initialChatState() });
      setConnection('disconnected');
      return;
    }
    let cancelled = false;
    lastSeq.current = cache.current.get(chatId)?.seq ?? 0;
    setCachedState({ chatId, state: cache.current.get(chatId) ?? initialChatState() });

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
          push(chatId, JSON.parse(msg.data) as Event);
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
    };
  }, [chatId, push]);

  const resnapshot = useCallback(async () => {
    if (!chatId) return;
    const snap = await api.snapshot(chatId);
    const next = applySnapshot(snap);
    cache.current.set(chatId, next);
    lastSeq.current = snap.seq;
    if (activeChatId.current === chatId) setCachedState({ chatId, state: next });
  }, [chatId]);

  const setState = useCallback((next: ChatState | ((previous: ChatState) => ChatState)) => {
    const targetChatId = activeChatId.current;
    setCachedState((current) => {
      const previous = current.chatId === targetChatId
        ? current.state
        : (targetChatId ? cache.current.get(targetChatId) : undefined) ?? initialChatState();
      const resolved = typeof next === 'function' ? next(previous) : next;
      if (targetChatId) cache.current.set(targetChatId, resolved);
      return { chatId: targetChatId, state: resolved };
    });
  }, []);

  return { state, connection, resnapshot, setState };
}
