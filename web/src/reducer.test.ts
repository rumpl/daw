import { describe, expect, it } from 'vitest';
import { applySnapshot, initialChatState, itemKey, reduce } from './reducer';
import type { Event, Snapshot } from './protocol.gen';

const emptyQueue = { steerDepth: 0, steerCapacity: 8, followUpDepth: 0, followUpCapacity: 8, steer: [], followUps: [] };

function ev(partial: Partial<Event> & Pick<Event, 'type'>): Event {
  return { seq: 0, ...partial } as Event;
}

describe('reducer', () => {
  it('accumulates assistant and reasoning deltas on one stable item', () => {
    let s = initialChatState();
    s = reduce(s, ev({ type: 'message_item', seq: 1, message: { id: 'a1', role: 'assistant', agentName: 'root', text: '', reasoning: '', streaming: true, createdAt: '', model: 'm' } }));
    s = reduce(s, ev({ type: 'assistant_delta', seq: 2, delta: { itemId: 'a1', text: 'Hel' } }));
    s = reduce(s, ev({ type: 'assistant_delta', seq: 3, delta: { itemId: 'a1', text: 'lo' } }));
    s = reduce(s, ev({ type: 'reasoning_delta', seq: 4, delta: { itemId: 'a1', text: 'why' } }));
    s = reduce(s, ev({ type: 'assistant_end', seq: 5, ref: { itemId: 'a1' } }));

    expect(s.items).toHaveLength(1);
    expect(s.items[0]?.message?.text).toBe('Hello');
    expect(s.items[0]?.message?.reasoning).toBe('why');
    expect(s.items[0]?.message?.streaming).toBe(false);
    expect(s.seq).toBe(5);
  });

  it('ignores replayed events it has already applied', () => {
    let s = initialChatState();
    s = reduce(s, ev({ type: 'assistant_delta', seq: 5, delta: { itemId: 'a1', text: 'x' } }));
    const before = s;
    s = reduce(s, ev({ type: 'assistant_delta', seq: 3, delta: { itemId: 'a1', text: 'y' } }));
    expect(s).toBe(before);
  });

  it('reconciles tool activity by ID without duplicating', () => {
    let s = initialChatState();
    for (const state of ['pending', 'running', 'success'] as const) {
      s = reduce(
        s,
        ev({
          type: state === 'success' ? 'tool_end' : 'tool_update',
          seq: s.seq + 1,
          tool: { id: 't1', name: 'shell', category: '', agentName: 'root', argsSummary: 'ls', state, preview: '', truncated: false, outputBytes: 0, isError: false },
        }),
      );
    }
    expect(s.items).toHaveLength(1);
    expect(s.items[0]?.tool?.state).toBe('success');
  });

  it('appends live tool output chunks and accepts the final complete response', () => {
    let s = initialChatState();
    s = reduce(s, ev({
      type: 'tool_start', seq: 1,
      tool: { id: 't1', name: 'shell', category: 'shell', agentName: 'root', argsSummary: 'run', state: 'running', preview: '', truncated: false, outputBytes: 0, isError: false },
    }));
    s = reduce(s, ev({
      type: 'tool_update', seq: 2,
      tool: { id: 't1', name: 'shell', category: '', agentName: '', argsSummary: '', state: 'running', preview: 'first\n', truncated: false, outputBytes: 6, isError: false },
    }));
    s = reduce(s, ev({
      type: 'tool_update', seq: 3,
      tool: { id: 't1', name: 'shell', category: '', agentName: '', argsSummary: '', state: 'running', preview: 'second\n', truncated: false, outputBytes: 7, isError: false },
    }));

    expect(s.items[0]?.tool?.preview).toBe('first\nsecond\n');
    expect(s.items[0]?.tool?.outputBytes).toBe(13);

    s = reduce(s, ev({
      type: 'tool_end', seq: 4,
      tool: { id: 't1', name: 'shell', category: '', agentName: '', argsSummary: '', state: 'success', preview: 'complete output', truncated: false, outputBytes: 15, isError: false },
    }));
    expect(s.items[0]?.tool?.preview).toBe('complete output');
    expect(s.items[0]?.tool?.outputBytes).toBe(15);
  });

  it('shows a live compaction summary and reconciles it by ID', () => {
    let s = initialChatState();
    const summary = { id: 'session-sum-4', text: 'The compacted conversation result.', cost: 0.0042 };
    s = reduce(s, ev({ type: 'summary', seq: 1, summary }));
    s = reduce(s, ev({ type: 'summary', seq: 2, summary }));

    expect(s.items).toEqual([{ kind: 'summary', summary }]);
  });

  it('resnapshot after reconnect replaces state without duplicates', () => {
    let s = initialChatState();
    s = reduce(s, ev({ type: 'message_item', seq: 1, message: { id: 'm1', role: 'user', agentName: '', text: 'hi', reasoning: '', streaming: false, createdAt: '', model: '' } }));
    const snapshot: Snapshot = {
      seq: 9,
      meta: null as never,
      items: [
        { kind: 'message', message: { id: 'm1', role: 'user', agentName: '', text: 'hi', reasoning: '', streaming: false, createdAt: '', model: '' } },
        { kind: 'message', message: { id: 'm2', role: 'assistant', agentName: 'root', text: 'yo', reasoning: '', streaming: false, createdAt: '', model: '' } },
      ],
      run: { state: 'idle', runId: '', queue: emptyQueue },
      usage: { inputTokens: 1, outputTokens: 2, cost: 0.1, contextLimit: 0 },
      pendingConfirmations: [],
      pendingElicitations: [],
    };
    s = reduce(s, ev({ type: 'snapshot', seq: 9, snapshot }));
    expect(s.items).toHaveLength(2);
    expect(new Set(s.items.map(itemKey)).size).toBe(2);
    expect(s.seq).toBe(9);
  });

  it('tracks pending confirmations and elicitations by id', () => {
    let s = initialChatState();
    s = reduce(s, ev({ type: 'tool_confirmation', seq: 1, confirmation: { toolCallId: 'c1', toolName: 'shell', agentName: 'root', argsSummary: 'ls', pattern: 'shell:cmd=ls*', patternLabel: 'always allow ls*', rejectionReasons: [] } }));
    // A duplicate (replayed) request must not create a second dialog.
    s = reduce(s, ev({ type: 'tool_confirmation', seq: 2, confirmation: { toolCallId: 'c1', toolName: 'shell', agentName: 'root', argsSummary: 'ls', pattern: 'shell:cmd=ls*', patternLabel: 'always allow ls*', rejectionReasons: [] } }));
    expect(s.confirmations).toHaveLength(1);

    s = reduce(s, ev({ type: 'tool_confirmation_resolved', seq: 3, toolResolved: { toolCallId: 'c1', decision: 'approve', pattern: 'shell:cmd=ls*' } }));
    expect(s.confirmations).toHaveLength(0);

    s = reduce(s, ev({ type: 'elicitation', seq: 4, elicitation: { elicitationId: 'e1', message: 'q', mode: 'form', url: '', agentName: 'root' } }));
    s = reduce(s, ev({ type: 'elicitation', seq: 5, elicitation: { elicitationId: 'e2', message: 'q2', mode: 'form', url: '', agentName: 'root' } }));
    expect(s.elicitations.map((e) => e.elicitationId)).toEqual(['e1', 'e2']);
    // Resolution is by id, so the right dialog closes.
    s = reduce(s, ev({ type: 'elicitation_resolved', seq: 6, elicitResolved: { elicitationId: 'e1' } }));
    expect(s.elicitations.map((e) => e.elicitationId)).toEqual(['e2']);
  });

  it('marks the chat closed', () => {
    let s = initialChatState();
    s = reduce(s, ev({ type: 'chat_closed', seq: 1, closed: { reason: 'disposed' } }));
    expect(s.closed).toBe(true);
    expect(s.closedReason).toBe('disposed');
  });

  it('applySnapshot tolerates missing arrays', () => {
    const s = applySnapshot({
      seq: 1,
      meta: null as never,
      items: null,
      run: { state: 'idle', runId: '', queue: emptyQueue },
      usage: { inputTokens: 0, outputTokens: 0, cost: 0, contextLimit: 0 },
      pendingConfirmations: null,
      pendingElicitations: null,
    });
    expect(s.items).toEqual([]);
    expect(s.confirmations).toEqual([]);
  });
});
