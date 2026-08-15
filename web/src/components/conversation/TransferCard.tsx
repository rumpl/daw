import type { Transfer } from '@/protocol.gen';
import { clip } from '@/safety';

export function TransferCard({ transfer }: { transfer: Transfer }) {
  return (
    <p className="transfer" aria-label="sub-agent transfer">
      <span>{clip(transfer.fromAgent, 60) || 'agent'} → <strong>{clip(transfer.toAgent, 60)}</strong></span>
    </p>
  );
}
