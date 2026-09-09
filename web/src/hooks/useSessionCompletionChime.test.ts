import { describe, expect, it } from 'vitest';
import type { SessionSummary } from '@/protocol.gen';
import { completedUnobservedSession } from './useSessionCompletionChime';

function session(sessionId: string, runState: SessionSummary['runState']): SessionSummary {
  return {
    sessionId,
    title: sessionId,
    workingDir: '/project',
    createdAt: '',
    messages: 0,
    starred: false,
    live: true,
    runState,
  };
}

describe('completedUnobservedSession', () => {
  it('detects a background tab changing from running to idle', () => {
    const previous = new Map<string, SessionSummary['runState']>([['background', 'running']]);
    expect(completedUnobservedSession(previous, [session('background', 'idle')], 'active', true)).toBe(true);
  });

  it('does not chime when the completed session is visible and active', () => {
    const previous = new Map<string, SessionSummary['runState']>([['active', 'running']]);
    expect(completedUnobservedSession(previous, [session('active', 'idle')], 'active', true)).toBe(false);
  });

  it('chimes for the active session when the page is hidden', () => {
    const previous = new Map<string, SessionSummary['runState']>([['active', 'stopping']]);
    expect(completedUnobservedSession(previous, [session('active', 'idle')], 'active', false)).toBe(true);
  });

  it('ignores newly discovered idle sessions', () => {
    expect(completedUnobservedSession(new Map(), [session('new', 'idle')], null, true)).toBe(false);
  });
});
