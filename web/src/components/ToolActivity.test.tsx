import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { ToolActivity } from '../protocol.gen';
import { defaultToolRendererNames, ToolCard } from './ToolActivity';

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

  it('uses docker-agent’s human-readable title and keeps the technical name as context', () => {
    render(<ToolCard tool={tool({ name: 'read_file', category: 'filesystem', displayName: 'Read', argsSummary: 'README.md', arguments: { path: 'README.md' } })} />);
    expect(screen.getByText('Read')).toBeVisible();
    expect(screen.getByText('read_file')).toBeVisible();
    expect(screen.getByText('README.md')).toBeVisible();
  });

  it('renders shell calls as a command and terminal output', () => {
    const { container } = render(<ToolCard tool={tool({ displayName: 'Shell', arguments: { cmd: 'npm test', cwd: 'web', timeout: 60 }, argsSummary: 'npm test', preview: '12 tests passed' })} />);
    container.querySelector('details')?.setAttribute('open', '');
    expect(screen.getAllByText('npm test')).toHaveLength(1);
    expect(screen.getByText('12 tests passed')).toBeVisible();
    expect(screen.queryByText('web')).not.toBeInTheDocument();
    expect(screen.queryByText('timeout 60s')).not.toBeInTheDocument();
  });

  it('turns list_directory output into file and directory entries', () => {
    const { container } = render(<ToolCard tool={tool({ name: 'list_directory', category: 'filesystem', displayName: 'List Directory', arguments: { path: '.' }, preview: 'DIR  src\nFILE package.json\n' })} />);
    container.querySelector('details')?.setAttribute('open', '');
    expect(screen.getByText('src')).toBeVisible();
    expect(screen.getByText('package.json')).toBeVisible();
  });

  it('shows the contents being written by write_file', () => {
    const { container } = render(<ToolCard tool={tool({ name: 'write_file', category: 'filesystem', displayName: 'Write', arguments: { path: 'hello.txt', contentBytes: 12, contentLines: 1, contentPreview: 'hello world!' }, preview: 'File written successfully.' })} />);
    container.querySelector('details')?.setAttribute('open', '');
    expect(screen.getByText('hello world!')).toBeVisible();
    expect(screen.getByText('File contents')).toBeVisible();
  });

  it('renders image attachments without expanding the tool', () => {
    render(<ToolCard tool={tool({
      name: 'read_file',
      preview: 'Read image file screenshot.png',
      images: [{ name: 'screenshot.png', mimeType: 'image/png', data: 'iVBORw0KGgo=' }],
    })} />);
    const image = screen.getByRole('img', { name: 'screenshot.png' });
    expect(image).toHaveAttribute('src', 'data:image/png;base64,iVBORw0KGgo=');
    expect(screen.getByText('screenshot.png · image/png')).toBeVisible();
  });

  it('renders edit previews as removed and added text', () => {
    const { container } = render(<ToolCard tool={tool({ name: 'edit_file', category: 'filesystem', displayName: 'Edit', arguments: { path: 'app.ts', editCount: 1, edits: [{ oldText: 'const old = true', newText: 'const next = true', removedLines: 1, addedLines: 1 }] }, preview: 'File edited successfully.' })} />);
    container.querySelector('details')?.setAttribute('open', '');
    expect(screen.getByText('const old = true')).toHaveClass('diff-remove');
    expect(screen.getByText('const next = true')).toHaveClass('diff-add');
  });
});
