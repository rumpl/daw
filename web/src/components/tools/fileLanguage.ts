const languageByExtension: Record<string, string> = {
  c: 'c', cc: 'cpp', cpp: 'cpp', cs: 'csharp', css: 'css', go: 'go', h: 'c', hpp: 'cpp',
  html: 'html', java: 'java', js: 'javascript', jsx: 'jsx', json: 'json', md: 'markdown',
  py: 'python', rb: 'ruby', rs: 'rust', sh: 'bash', sql: 'sql', ts: 'typescript', tsx: 'tsx',
  xml: 'xml', yaml: 'yaml', yml: 'yaml',
};

export function languageForPath(path: string): string | undefined {
  const extension = path.split('.').pop()?.toLowerCase();
  return extension ? languageByExtension[extension] : undefined;
}
