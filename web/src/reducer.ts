// Client-side mirror of the server reducer.
//
// Reconciliation is by stable docker-agent item IDs only: never by text,
// never by timestamp. That is what makes an SSE reconnect (replay or
// resnapshot) produce no duplicates.
import type {
  ElicitationRequest,
  Event,
  Item,
  QueueStatus,
  RunStatus,
  SessionMeta,
  Snapshot,
  ToolConfirmationRequest,
  Usage,
} from './protocol.gen';

export type ConnectionState = 'connecting' | 'connected' | 'reconnecting' | 'disconnected';

export interface ChatState {
  seq: number;
  items: Item[];
  meta: SessionMeta | null;
  run: RunStatus;
  usage: Usage;
  confirmations: ToolConfirmationRequest[];
  elicitations: ElicitationRequest[];
  closed: boolean;
  closedReason: string;
}

const emptyQueue: QueueStatus = {
  steerDepth: 0,
  steerCapacity: 0,
  followUpDepth: 0,
  followUpCapacity: 0,
};

export function initialChatState(): ChatState {
  return {
    seq: 0,
    items: [],
    meta: null,
    run: { state: 'idle', runId: '', queue: emptyQueue },
    usage: { inputTokens: 0, outputTokens: 0, cost: 0, contextLimit: 0 },
    confirmations: [],
    elicitations: [],
    closed: false,
    closedReason: '',
  };
}

export function itemKey(item: Item): string {
  switch (item.kind) {
    case 'message':
      return `m:${item.message?.id ?? ''}`;
    case 'tool':
      return `t:${item.tool?.id ?? ''}`;
    case 'transfer':
      return `x:${item.transfer?.id ?? ''}`;
    case 'notice':
      return `n:${item.notice?.id ?? ''}`;
    case 'summary':
      return `s:${item.summary?.id ?? ''}`;
    default:
      return '';
  }
}

function upsert(items: Item[], item: Item): Item[] {
  const key = itemKey(item);
  if (!key) return items;
  const idx = items.findIndex((existing) => itemKey(existing) === key);
  if (idx === -1) return [...items, item];
  const next = items.slice();
  next[idx] = item;
  return next;
}

function upsertTool(items: Item[], tool: NonNullable<Item['tool']>): Item[] {
  const key = `t:${tool.id}`;
  const idx = items.findIndex((existing) => itemKey(existing) === key);
  if (idx === -1) return [...items, { kind: 'tool', tool }];
  const current = items[idx]?.tool;
  if (!current) return items;
  const merged = {
    ...current,
    ...tool,
    // Incremental output events may omit the call-only presentation fields.
    displayName: tool.displayName || current.displayName,
    argsSummary: tool.argsSummary || current.argsSummary,
    arguments: tool.arguments ?? current.arguments,
  };
  const next = items.slice();
  next[idx] = { kind: 'tool', tool: merged };
  return next;
}

function patchMessage(items: Item[], id: string, patch: (text: string, reasoning: string) => [string, string, boolean]): Item[] {
  const key = `m:${id}`;
  const idx = items.findIndex((existing) => itemKey(existing) === key);
  if (idx === -1) return items;
  const current = items[idx];
  if (!current?.message) return items;
  const [text, reasoning, streaming] = patch(current.message.text, current.message.reasoning);
  const next = items.slice();
  next[idx] = { ...current, message: { ...current.message, text, reasoning, streaming } };
  return next;
}

export function applySnapshot(snapshot: Snapshot): ChatState {
  return {
    seq: snapshot.seq,
    items: snapshot.items ?? [],
    meta: snapshot.meta,
    run: snapshot.run,
    usage: snapshot.usage,
    confirmations: snapshot.pendingConfirmations ?? [],
    elicitations: snapshot.pendingElicitations ?? [],
    closed: false,
    closedReason: '',
  };
}

export function reduce(state: ChatState, event: Event): ChatState {
  // Stale or duplicated events (a replay overlapping what we already saw)
  // are ignored by sequence number.
  if (event.seq > 0 && event.seq <= state.seq && event.type !== 'snapshot') {
    return state;
  }
  const seq = event.seq > 0 ? event.seq : state.seq;

  switch (event.type) {
    case 'snapshot':
      return event.snapshot ? applySnapshot(event.snapshot) : state;
    case 'run_status':
      return event.run ? { ...state, seq, run: event.run } : { ...state, seq };
    case 'session_meta':
      return event.meta ? { ...state, seq, meta: event.meta } : { ...state, seq };
    case 'usage':
      return event.usage ? { ...state, seq, usage: event.usage } : { ...state, seq };
    case 'message_item':
      return event.message
        ? { ...state, seq, items: upsert(state.items, { kind: 'message', message: event.message }) }
        : { ...state, seq };
    case 'assistant_delta':
      return event.delta
        ? {
            ...state,
            seq,
            items: patchMessage(state.items, event.delta.itemId, (text, reasoning) => [
              text + (event.delta?.text ?? ''),
              reasoning,
              true,
            ]),
          }
        : { ...state, seq };
    case 'reasoning_delta':
      return event.delta
        ? {
            ...state,
            seq,
            items: patchMessage(state.items, event.delta.itemId, (text, reasoning) => [
              text,
              reasoning + (event.delta?.text ?? ''),
              true,
            ]),
          }
        : { ...state, seq };
    case 'assistant_end':
    case 'reasoning_end':
      return event.ref
        ? {
            ...state,
            seq,
            items: patchMessage(state.items, event.ref.itemId, (text, reasoning) => [text, reasoning, false]),
          }
        : { ...state, seq };
    case 'tool_start':
    case 'tool_update':
    case 'tool_end':
      return event.tool
        ? { ...state, seq, items: upsertTool(state.items, event.tool) }
        : { ...state, seq };
    case 'transfer':
      return event.transfer
        ? { ...state, seq, items: upsert(state.items, { kind: 'transfer', transfer: event.transfer }) }
        : { ...state, seq };
    case 'notice':
      return event.notice
        ? { ...state, seq, items: upsert(state.items, { kind: 'notice', notice: event.notice }) }
        : { ...state, seq };
    case 'tool_confirmation':
      if (!event.confirmation) return { ...state, seq };
      if (state.confirmations.some((c) => c.toolCallId === event.confirmation?.toolCallId)) {
        return { ...state, seq };
      }
      return { ...state, seq, confirmations: [...state.confirmations, event.confirmation] };
    case 'tool_confirmation_resolved':
      return {
        ...state,
        seq,
        confirmations: state.confirmations.filter((c) => c.toolCallId !== event.toolResolved?.toolCallId),
      };
    case 'elicitation':
      if (!event.elicitation) return { ...state, seq };
      if (state.elicitations.some((e) => e.elicitationId === event.elicitation?.elicitationId)) {
        return { ...state, seq };
      }
      return { ...state, seq, elicitations: [...state.elicitations, event.elicitation] };
    case 'elicitation_resolved':
      return {
        ...state,
        seq,
        elicitations: state.elicitations.filter((e) => e.elicitationId !== event.elicitResolved?.elicitationId),
      };
    case 'chat_closed':
      return { ...state, seq, closed: true, closedReason: event.closed?.reason ?? '' };
    case 'gap':
      // The server will follow up with a full snapshot.
      return state;
    default:
      return { ...state, seq };
  }
}
