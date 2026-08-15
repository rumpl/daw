import type { ToolArgs } from './types';

export const text = (args: ToolArgs, key: string) => typeof args[key] === 'string' ? args[key] as string : '';
export const number = (args: ToolArgs, key: string) => typeof args[key] === 'number' ? args[key] as number : undefined;
export const strings = (args: ToolArgs, key: string) => Array.isArray(args[key])
  ? (args[key] as unknown[]).filter((value): value is string => typeof value === 'string')
  : [];
export const pathSummary = (args: ToolArgs, fallback: string) => text(args, 'path') || fallback;

export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(bytes < 10 * 1024 ? 1 : 0)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function filesSummary(args: ToolArgs, fallback: string): string {
  const paths = strings(args, 'paths');
  if (!paths.length) return fallback;
  if (paths.length === 1) return paths[0] ?? fallback;
  return `${paths.length}${number(args, 'pathsTruncated') ? '+' : ''} paths · ${paths.slice(0, 2).join(', ')}`;
}
