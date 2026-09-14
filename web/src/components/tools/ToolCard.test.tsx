import { fireEvent, render, screen, waitFor } from '@/test-utils';
import { describe, expect, it } from 'vitest';
import type { ToolActivity } from '@/protocol.gen';
import { ToolCard } from './ToolCard';
import { defaultToolRendererNames } from './toolRenderers';
import { languageForPath } from './fileLanguage';

function tool(overrides: Partial<ToolActivity>): ToolActivity {
  return {
    id: 'tool-1',
    name: 'shell',
    category: 'shell',
    agentName: 'coder',
    argsSummary: '',
    state: 'success',
    preview: '',
    truncated: false,
    outputBytes: 0,
    isError: false,
    ...overrides,
  };
}

const defaults = [
  'shell',
  'directory_tree',
  'edit_file',
  'list_directory',
  'read_file',
  'read_multiple_files',
  'search_files_content',
  'write_file',
  'create_directory',
  'remove_directory',
];

describe('ToolCard', () => {
  it('has a dedicated renderer for every tool in the default shell and filesystem toolsets', () => {
    expect([...defaultToolRendererNames].sort()).toEqual([...defaults].sort());
  });

  it('renders an animated disclosure hint for the clickable trigger', () => {
    const { container } = render(<ToolCard tool={tool({ displayName: 'Shell' })} />);
    const trigger = container.querySelector('.tool-trigger')!;

    expect(trigger).toHaveTextContent('View details');
    expect(trigger).toHaveTextContent('Hide details');
    expect(trigger.querySelector('.tool-click-hint')).toHaveAttribute('aria-hidden', 'true');

    fireEvent.click(trigger);
    expect(trigger).toHaveAttribute('data-panel-open');
  });

  it('uses the human-readable title and path without repeating a known technical name', () => {
    render(<ToolCard tool={tool({ name: 'read_file', category: 'filesystem', displayName: 'Read', argsSummary: 'README.md', arguments: { path: 'README.md' } })} />);
    expect(screen.getByText('Read')).toBeVisible();
    expect(screen.queryByText('read_file')).not.toBeInTheDocument();
    expect(screen.getByText('README.md')).toBeVisible();
    expect(screen.queryByText('Done')).not.toBeInTheDocument();
  });

  it('renders shell calls as a command and terminal output', () => {
    const { container } = render(<ToolCard tool={tool({ displayName: 'Shell', arguments: { cmd: 'npm test', cwd: 'web', timeout: 60 }, argsSummary: 'npm test', preview: '12 tests passed' })} />);
    fireEvent.click(container.querySelector('.tool-trigger')!);
    expect(container.querySelector('.shell-input')).toHaveTextContent('npm test');
    expect(screen.getByText('12 tests passed')).toBeVisible();
    expect(screen.queryByText('web')).not.toBeInTheDocument();
    expect(screen.queryByText('timeout 60s')).not.toBeInTheDocument();
  });

  it('renders the complete multiline shell command above its output', () => {
    const command = "node --input-type=module <<'NODE'\nconst answer = 42;\nconsole.log(answer);\nNODE";
    const { container } = render(<ToolCard tool={tool({ name: 'shell', displayName: 'Shell', arguments: { cmd: command, cwd: 'web', timeout: 60 }, argsSummary: "node --input-type=module <<'NODE'", preview: '42' })} />);
    fireEvent.click(container.querySelector('.tool-trigger')!);

    expect(container.querySelector('.shell-command')).toHaveAccessibleName('Command');
    expect(container.querySelector('.shell-output-section')).toHaveAccessibleName('Output');
    expect(container.querySelector('.shell-prompt')).toHaveTextContent('$');
    expect(container.querySelector('.shell-input')?.textContent).toBe(command);
    expect(container.querySelector('.shell-output')).toHaveTextContent('42');
  });

  it('supports command as the shell input alias', () => {
    const { container } = render(<ToolCard tool={tool({ name: 'shell', displayName: 'Shell', arguments: { command: 'printf one\\nprintf two' }, preview: 'onetwo' })} />);
    fireEvent.click(container.querySelector('.tool-trigger')!);
    expect(container.querySelector('.shell-input')).toHaveTextContent('printf one\\nprintf two');
  });

  it('turns list_directory output into file and directory entries', () => {
    const { container } = render(<ToolCard tool={tool({ name: 'list_directory', category: 'filesystem', displayName: 'List Directory', arguments: { path: '.' }, preview: 'DIR  src\nFILE package.json\n' })} />);
    fireEvent.click(container.querySelector('.tool-trigger')!);
    expect(screen.getByText('src')).toBeVisible();
    expect(screen.getByText('package.json')).toBeVisible();
  });

  it('uses highlight.js aliases and its full language registry for file types', () => {
    expect(languageForPath('electron/main.cjs')).toBe('cjs');
    expect(languageForPath('src/types.d.cts')).toBe('cts');
    expect(languageForPath('config.toml')).toBe('toml');
    expect(languageForPath('Dockerfile')).toBe('dockerfile');
    expect(languageForPath('file.unknown')).toBeUndefined();
  });

  it('syntax-highlights read_file output for known file extensions', () => {
    const { container } = render(<ToolCard tool={tool({ name: 'read_file', category: 'filesystem', displayName: 'Read', arguments: { path: 'electron/main.cjs', line: 4, limit: 1 }, preview: 'const answer = 42;' })} />);
    fireEvent.click(container.querySelector('.tool-trigger')!);
    expect(container.querySelector('.file-code code.language-cjs')).not.toBeNull();
    expect(container.querySelector('.file-code .hljs-keyword')?.textContent).toBe('const');
    expect(screen.getByText('Contents · lines 4–4')).toBeVisible();
  });

  it('leaves read_file output plain for unknown file extensions', () => {
    const { container } = render(<ToolCard tool={tool({ name: 'read_file', category: 'filesystem', displayName: 'Read', arguments: { path: 'hello.unknown' }, preview: 'plain contents' })} />);
    fireEvent.click(container.querySelector('.tool-trigger')!);
    expect(container.querySelector('.file-code')).toBeNull();
    expect(screen.getByText('plain contents')).toBeVisible();
  });

  it('shows the contents being written by write_file', () => {
    const { container } = render(<ToolCard tool={tool({ name: 'write_file', category: 'filesystem', displayName: 'Write', arguments: { path: 'hello.txt', contentBytes: 12, contentLines: 1, contentPreview: 'hello world!' }, preview: 'File written successfully.' })} />);
    fireEvent.click(container.querySelector('.tool-trigger')!);
    expect(screen.getByText('hello world!')).toBeVisible();
    expect(screen.getByText('File contents')).toBeVisible();
  });

  it('syntax-highlights write_file previews for known file extensions', () => {
    const { container } = render(<ToolCard tool={tool({ name: 'write_file', category: 'filesystem', displayName: 'Write', arguments: { path: 'hello.ts', contentBytes: 20, contentLines: 1, contentPreview: 'const answer = 42;' }, preview: 'File written successfully.' })} />);
    fireEvent.click(container.querySelector('.tool-trigger')!);
    expect(container.querySelector('.write-code code.language-ts')).not.toBeNull();
    expect(container.querySelector('.write-code .hljs-keyword')?.textContent).toBe('const');
  });

  it('leaves write_file previews plain for unknown file extensions', () => {
    const { container } = render(<ToolCard tool={tool({ name: 'write_file', category: 'filesystem', displayName: 'Write', arguments: { path: 'hello.unknown', contentPreview: 'plain contents' }, preview: 'File written successfully.' })} />);
    fireEvent.click(container.querySelector('.tool-trigger')!);
    expect(container.querySelector('.write-code')).toBeNull();
    expect(screen.getByText('plain contents')).toBeVisible();
  });

  it('opens image attachments full size without expanding the tool', () => {
    render(<ToolCard tool={tool({
      name: 'read_file',
      preview: 'Read image file screenshot.png',
      images: [{ name: 'screenshot.png', mimeType: 'image/png', data: 'iVBORw0KGgo=' }],
    })} />);
    const image = screen.getByRole('img', { name: 'screenshot.png' });
    expect(image).toHaveAttribute('src', 'data:image/png;base64,iVBORw0KGgo=');
    expect(screen.getByText('screenshot.png · image/png')).toBeVisible();

    fireEvent.click(screen.getByRole('button', { name: 'View screenshot.png full size' }));
    const dialog = screen.getByRole('dialog', { name: 'screenshot.png full size' });
    expect(dialog).toBeVisible();
    expect(dialog.querySelector('img')).toHaveAttribute('src', 'data:image/png;base64,iVBORw0KGgo=');
  });

  it('renders edit previews as a split, line-aligned diff', async () => {
    const { container } = render(<ToolCard tool={tool({ name: 'edit_file', category: 'filesystem', displayName: 'Edit', arguments: { path: 'app.ts', editCount: 1, edits: [{ oldText: 'const shared = true;\nconst old = true;', newText: 'const shared = true;\nconst next = true;', removedLines: 1, addedLines: 1 }] }, preview: 'File edited successfully.' })} />);
    fireEvent.click(container.querySelector('.tool-trigger')!);
    const diff = screen.getByLabelText('Split diff for change 1');
    expect(diff).toHaveTextContent('Before');
    expect(diff).toHaveTextContent('After');
    expect(diff.querySelector('table')).not.toBeNull();
    await waitFor(() => expect(diff).toHaveTextContent('const old = true;'));
    expect(diff).toHaveTextContent('const next = true;');
    expect(diff.textContent?.match(/const shared = true;/g)).toHaveLength(2);
    expect(screen.queryByText('File edited successfully.')).not.toBeInTheDocument();
  });
});
