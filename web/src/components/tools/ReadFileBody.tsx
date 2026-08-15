import type { ToolActivity } from '@/protocol.gen';
import { PlainOutput } from './PlainOutput';
import type { ToolArgs } from './types';
import { number } from './utils';

export function ReadFileBody({ tool, args }: { tool: ToolActivity; args: ToolArgs }) {
  const line = number(args, 'line');
  const limit = number(args, 'limit');
  const label = line ? `Contents · lines ${line}${limit ? `–${line + limit - 1}` : '+'}` : 'Contents';
  return <PlainOutput tool={tool} label={label} />;
}
