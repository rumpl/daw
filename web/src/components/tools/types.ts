import type { ToolActivity } from '@/protocol.gen';
import type { ReactNode } from 'react';

export type ToolArgs = Record<string, unknown>;

export interface ToolRenderer {
  title: string;
  summary: (args: ToolArgs, fallback: string) => string;
  body: (tool: ToolActivity, args: ToolArgs) => ReactNode;
}
