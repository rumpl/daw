import type { ChatState } from '@/reducer';
import { ElicitationDialog } from './ElicitationDialog';
import { ToolConfirmDialog } from './ToolConfirmDialog';

type ToolDecision = Parameters<typeof ToolConfirmDialog>[0]['onDecide'];
type ElicitationAnswer = Parameters<typeof ElicitationDialog>[0]['onAnswer'];

interface PendingDialogsProps {
  state: ChatState;
  onToolDecision: ToolDecision;
  onElicitationAnswer: ElicitationAnswer;
}

export function PendingDialogs({ state, onToolDecision, onElicitationAnswer }: PendingDialogsProps) {
  if (state.confirmations[0]) {
    return <ToolConfirmDialog request={state.confirmations[0]} onDecide={onToolDecision} />;
  }
  if (state.elicitations[0]) {
    return <ElicitationDialog request={state.elicitations[0]} onAnswer={onElicitationAnswer} />;
  }
  return null;
}
