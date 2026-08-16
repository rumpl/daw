import { Box, Laptop } from 'lucide-react';
import type { ExecutionTarget } from '@/protocol.gen';

export function executionTargetLabel(target?: ExecutionTarget): string {
  return target === 'sandbox' ? 'Docker Sandbox' : target === 'host' ? 'Host' : 'Execution target unavailable';
}

export function ExecutionTargetIcon({ target, className }: {
  target?: ExecutionTarget;
  className?: string;
}) {
  if (target === 'sandbox') return <Box className={className} aria-hidden="true" />;
  if (target === 'host') return <Laptop className={className} aria-hidden="true" />;
  return null;
}
