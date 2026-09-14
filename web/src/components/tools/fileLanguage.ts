import { all, createLowlight } from 'lowlight';

const highlighter = createLowlight(all);

export function languageForPath(path: string): string | undefined {
  const filename = path.split(/[\\/]/).pop()?.toLowerCase();
  if (!filename) return undefined;

  const candidates = [filename, ...filename.split('.').slice(1)];
  return candidates.reverse().find((candidate) => highlighter.registered(candidate));
}
