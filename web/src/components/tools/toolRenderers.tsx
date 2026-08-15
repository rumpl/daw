import { DirectoryTreeBody } from './DirectoryTreeBody';
import { EditFileBody } from './EditFileBody';
import { ListDirectoryBody } from './ListDirectoryBody';
import { PathsBody } from './PathsBody';
import { ReadFileBody } from './ReadFileBody';
import { ReadMultipleBody } from './ReadMultipleBody';
import { SearchBody } from './SearchBody';
import { ShellBody } from './ShellBody';
import type { ToolRenderer } from './types';
import { filesSummary, formatBytes, number, pathSummary, text } from './utils';
import { WriteFileBody } from './WriteFileBody';

export const toolRenderers: Record<string, ToolRenderer> = {
  shell: {
    title: 'Shell',
    summary: (args, fallback) => (text(args, 'cmd') || text(args, 'command') || fallback).split('\n')[0] ?? fallback,
    body: (tool) => <ShellBody tool={tool} />,
  },
  directory_tree: {
    title: 'Directory Tree', summary: pathSummary,
    body: (tool) => <DirectoryTreeBody tool={tool} />,
  },
  edit_file: {
    title: 'Edit',
    summary: (args, fallback) => `${pathSummary(args, fallback)}${number(args, 'editCount') !== undefined ? ` · ${number(args, 'editCount')} change${number(args, 'editCount') === 1 ? '' : 's'}` : ''}`,
    body: (tool, args) => <EditFileBody tool={tool} args={args} />,
  },
  list_directory: {
    title: 'List Directory', summary: pathSummary,
    body: (tool) => <ListDirectoryBody tool={tool} />,
  },
  read_file: {
    title: 'Read', summary: pathSummary,
    body: (tool, args) => <ReadFileBody tool={tool} args={args} />,
  },
  read_multiple_files: {
    title: 'Read Multiple Files', summary: filesSummary,
    body: (tool) => <ReadMultipleBody tool={tool} />,
  },
  search_files_content: {
    title: 'Search Files Content',
    summary: (args, fallback) => {
      const query = text(args, 'query');
      const path = text(args, 'path');
      return query ? `${query} · ${path || '.'}${args.is_regex === true ? ' · regex' : ''}` : fallback;
    },
    body: (tool, args) => <SearchBody tool={tool} args={args} />,
  },
  write_file: {
    title: 'Write',
    summary: (args, fallback) => {
      const path = pathSummary(args, fallback);
      const bytes = number(args, 'contentBytes');
      return bytes === undefined ? path : `${path} · ${formatBytes(bytes)}`;
    },
    body: (tool, args) => <WriteFileBody tool={tool} args={args} />,
  },
  create_directory: {
    title: 'Create Directory', summary: filesSummary,
    body: (tool, args) => <PathsBody tool={tool} args={args} verb="Created" />,
  },
  remove_directory: {
    title: 'Remove Directory', summary: filesSummary,
    body: (tool, args) => <PathsBody tool={tool} args={args} verb="Removed" />,
  },
};

export const defaultToolRendererNames = Object.freeze(Object.keys(toolRenderers));
