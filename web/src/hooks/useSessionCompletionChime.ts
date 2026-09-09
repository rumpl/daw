import { useEffect, useRef } from 'react';
import type { SessionSummary } from '@/protocol.gen';

let audioContext: AudioContext | null = null;

function getAudioContext() {
  if (typeof window === 'undefined' || !window.AudioContext) return null;
  audioContext ??= new window.AudioContext();
  return audioContext;
}

/** Unlock audio while handling a user gesture so later background completions can make a sound. */
export function enableCompletionChime() {
  const context = getAudioContext();
  if (context?.state === 'suspended') void context.resume().catch(() => undefined);
}

export function playCompletionChime() {
  const context = getAudioContext();
  if (!context || context.state !== 'running') return;

  const start = context.currentTime;
  const master = context.createGain();
  master.gain.setValueAtTime(0.0001, start);
  master.gain.exponentialRampToValueAtTime(0.14, start + 0.015);
  master.gain.exponentialRampToValueAtTime(0.0001, start + 1.35);
  master.connect(context.destination);

  for (const [frequency, delay, volume] of [
    [523.25, 0, 0.75],
    [659.25, 0.1, 0.55],
    [783.99, 0.2, 0.45],
  ] as const) {
    const oscillator = context.createOscillator();
    const gain = context.createGain();
    oscillator.type = 'sine';
    oscillator.frequency.setValueAtTime(frequency, start + delay);
    gain.gain.setValueAtTime(volume, start + delay);
    gain.gain.exponentialRampToValueAtTime(0.0001, start + delay + 1.05);
    oscillator.connect(gain);
    gain.connect(master);
    oscillator.start(start + delay);
    oscillator.stop(start + delay + 1.1);
  }
}

export function completedUnobservedSession(
  previousStates: ReadonlyMap<string, SessionSummary['runState']>,
  sessions: readonly SessionSummary[],
  activeSessionId: string | null,
  pageVisible: boolean,
) {
  return sessions.some((session) => {
    const previous = previousStates.get(session.sessionId);
    const completed = (previous === 'running' || previous === 'stopping') && session.runState === 'idle';
    const observed = pageVisible && session.sessionId === activeSessionId;
    return completed && !observed;
  });
}

export function useSessionCompletionChime(
  sessions: readonly SessionSummary[],
  activeSessionId: string | null,
) {
  const previousStates = useRef<Map<string, SessionSummary['runState']> | null>(null);

  useEffect(() => {
    window.addEventListener('pointerdown', enableCompletionChime, { once: true });
    window.addEventListener('keydown', enableCompletionChime, { once: true });
    return () => {
      window.removeEventListener('pointerdown', enableCompletionChime);
      window.removeEventListener('keydown', enableCompletionChime);
    };
  }, []);

  useEffect(() => {
    const nextStates = new Map(sessions.map((session) => [session.sessionId, session.runState]));
    const previous = previousStates.current;
    previousStates.current = nextStates;
    if (!previous) return;

    if (completedUnobservedSession(
      previous,
      sessions,
      activeSessionId,
      document.visibilityState === 'visible',
    )) {
      playCompletionChime();
    }
  }, [activeSessionId, sessions]);
}
