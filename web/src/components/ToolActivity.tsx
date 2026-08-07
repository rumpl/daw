import type { ReactNode } from 'react';
import type { ToolActivity } from '../protocol.gen';
import { clip } from '../safety';

type ToolArgs = Record<string, unknown>;
type ToolRenderer = {
  title: string;
  summary: (args: ToolArgs, fallback: string) => string;
  body: (tool: ToolActivity, args: ToolArgs) => ReactNode;
};

const text = (args: ToolArgs, key: string) => (typeof args[key] === 'string' ? (args[key] as string) : '');
const number = (args: ToolArgs, key: string) => (typeof args[key] === 'number' ? (args[key] as number) : undefined);
const strings = (args: ToolArgs, key: string) =>
  Array.isArray(args[key]) ? (args[key] as unknown[]).filter((value): value is string => typeof value === 'string') : [];
const pathSummary = (args: ToolArgs, fallback: string) => text(args, 'path') || fallback;

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(bytes < 10 * 1024 ? 1 : 0)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function ToolImages({ tool }: { tool: ToolActivity }) {
  if (!tool.images?.length) return null;
  return (
    <div className="tool-images">
      {tool.images.map((image, i) => (
        <figure key={`${image.name}-${i}`}>
          <a href={`data:${image.mimeType};base64,${image.data}`} target="_blank" rel="noreferrer">
            <img src={`data:${image.mimeType};base64,${image.data}`} alt={image.name || `Tool result image ${i + 1}`} loading="lazy" />
          </a>
          <figcaption>{image.name || `image-${i + 1}`} · {image.mimeType}</figcaption>
        </figure>
      ))}
    </div>
  );
}

function PlainOutput({ tool, label = 'Output' }: { tool: ToolActivity; label?: string }) {
  if (!tool.preview) return <p className="tool-empty">No output</p>;
  return (
    <div className="tool-result">
      <div className="tool-result-head">{label}</div>
      <pre className="tool-output" tabIndex={0}>{tool.preview}</pre>
    </div>
  );
}

function ShellBody({ tool }: { tool: ToolActivity }) {
  return (
    <div className="shell-result">
      {tool.preview ? <pre className="tool-output shell-output" tabIndex={0}>{tool.preview}</pre> : <p className="tool-empty">Waiting for output…</p>}
    </div>
  );
}

type TreeNode = { name?: unknown; type?: unknown; children?: unknown };
function isTreeNode(value: unknown): value is TreeNode {
  return typeof value === 'object' && value !== null && typeof (value as TreeNode).name === 'string';
}
function TreeBranch({ node, root = false }: { node: TreeNode; root?: boolean }) {
  const children = Array.isArray(node.children) ? node.children.filter(isTreeNode) : [];
  const directory = node.type === 'directory';
  return (
    <li className={root ? 'tree-root' : undefined}>
      <span className={`tree-entry tree-${directory ? 'dir' : 'file'}`}>
        <span aria-hidden="true">{directory ? '▾' : '·'}</span>{String(node.name)}
      </span>
      {children.length ? <ul>{children.map((child, i) => <TreeBranch key={`${String(child.name)}-${i}`} node={child} />)}</ul> : null}
    </li>
  );
}
function DirectoryTreeBody({ tool }: { tool: ToolActivity }) {
  if (tool.preview) {
    try {
      const tree: unknown = JSON.parse(tool.preview);
      if (isTreeNode(tree)) return <ul className="tool-tree"><TreeBranch node={tree} root /></ul>;
    } catch {
      // Streaming and truncated JSON intentionally falls through to raw text.
    }
  }
  return <PlainOutput tool={tool} label="Directory tree" />;
}

function ListDirectoryBody({ tool }: { tool: ToolActivity }) {
  const entries = tool.preview.split('\n').flatMap((line) => {
    const match = /^(DIR|FILE)\s+(.+)$/.exec(line);
    return match ? [{ directory: match[1] === 'DIR', name: match[2] ?? '' }] : [];
  });
  if (!entries.length) return <PlainOutput tool={tool} label="Directory listing" />;
  return (
    <ul className="directory-list">
      {entries.map((entry, i) => (
        <li key={`${entry.name}-${i}`} className={entry.directory ? 'directory' : 'file'}>
          <span aria-hidden="true">{entry.directory ? '▸' : '·'}</span><code>{entry.name}</code>
        </li>
      ))}
    </ul>
  );
}

function ReadFileBody({ tool, args }: { tool: ToolActivity; args: ToolArgs }) {
  const line = number(args, 'line');
  const limit = number(args, 'limit');
  const label = line ? `Contents · lines ${line}${limit ? `–${line + limit - 1}` : '+'}` : 'Contents';
  return <PlainOutput tool={tool} label={label} />;
}

type FileSection = { path: string; content: string };
function splitFileSections(preview: string): FileSection[] {
  const matches = [...preview.matchAll(/^=== (.+) ===$/gm)];
  return matches.map((match, i) => {
    const start = (match.index ?? 0) + match[0].length + 1;
    const end = matches[i + 1]?.index ?? preview.length;
    return { path: match[1] ?? '', content: preview.slice(start, end).replace(/\n\n$/, '') };
  });
}
function ReadMultipleBody({ tool }: { tool: ToolActivity }) {
  const sections = splitFileSections(tool.preview);
  if (!sections.length) return <PlainOutput tool={tool} label="Files" />;
  return (
    <div className="multi-file-output">
      {sections.map((section, i) => (
        <section key={`${section.path}-${i}`}>
          <header><code>{section.path}</code></header>
          <pre className="tool-output" tabIndex={0}>{section.content}</pre>
        </section>
      ))}
    </div>
  );
}

type SearchResult = { path: string; line: string; column: string; excerpt: string };
function SearchBody({ tool, args }: { tool: ToolActivity; args: ToolArgs }) {
  const results: SearchResult[] = tool.preview.split('\n').flatMap((row) => {
    const match = /^(.*):(\d+):(\d+):\s?(.*)$/.exec(row);
    return match ? [{ path: match[1] ?? '', line: match[2] ?? '', column: match[3] ?? '', excerpt: match[4] ?? '' }] : [];
  });
  return (
    <div className="search-output">
      {results.length ? results.map((result, i) => (
        <div className="search-hit" key={`${result.path}-${result.line}-${i}`}>
          <div><code>{result.path}</code><span>:{result.line}:{result.column}</span></div>
          <pre>{result.excerpt}</pre>
        </div>
      )) : <PlainOutput tool={tool} label={text(args, 'query') ? `Matches for “${clip(text(args, 'query'), 80)}”` : 'Matches'} />}
    </div>
  );
}

type EditPreview = { oldText?: unknown; newText?: unknown; removedLines?: unknown; addedLines?: unknown };
function EditFileBody({ tool, args }: { tool: ToolActivity; args: ToolArgs }) {
  const edits = Array.isArray(args.edits) ? (args.edits as EditPreview[]) : [];
  return (
    <div className="edit-output">
      {edits.map((edit, i) => (
        <section className="edit-block" key={i}>
          <header>Change {i + 1}<span>−{String(edit.removedLines ?? 0)} +{String(edit.addedLines ?? 0)} lines</span></header>
          {typeof edit.oldText === 'string' && edit.oldText ? <pre className="diff-remove">{edit.oldText}</pre> : null}
          {typeof edit.newText === 'string' && edit.newText ? <pre className="diff-add">{edit.newText}</pre> : null}
        </section>
      ))}
      {number(args, 'editsTruncated') ? <p className="tool-note">{number(args, 'editsTruncated')} more changes not shown.</p> : null}
      {tool.preview ? <div className="edit-status">{tool.preview}</div> : null}
      {!edits.length && !tool.preview ? <p className="tool-empty">Waiting for result…</p> : null}
    </div>
  );
}

function WriteFileBody({ tool, args }: { tool: ToolActivity; args: ToolArgs }) {
  const bytes = number(args, 'contentBytes');
  const lines = number(args, 'contentLines');
  const hasContentPreview = typeof args.contentPreview === 'string';
  const content = text(args, 'contentPreview');
  return (
    <div className="mutation-output">
      {bytes !== undefined || lines !== undefined ? (
        <div className="tool-stats">
          {lines !== undefined ? <span><strong>{lines}</strong> lines</span> : null}
          {bytes !== undefined ? <span><strong>{formatBytes(bytes)}</strong></span> : null}
        </div>
      ) : null}
      {hasContentPreview ? (
        <div className="write-preview">
          <div className="tool-result-head">File contents</div>
          <pre className={`tool-output${content ? '' : ' empty-file'}`} tabIndex={0}>{content || '(empty file)'}</pre>
          {args.contentTruncated === true ? <p className="tool-note">File preview truncated for display.</p> : null}
        </div>
      ) : null}
      <PlainOutput tool={tool} label="Result" />
    </div>
  );
}

function PathsBody({ tool, args, verb }: { tool: ToolActivity; args: ToolArgs; verb: string }) {
  const paths = strings(args, 'paths');
  return (
    <div className="paths-output">
      {paths.length ? <ul>{paths.map((path, i) => <li key={`${path}-${i}`}><span aria-hidden="true">{verb === 'Created' ? '+' : '−'}</span><code>{path}</code></li>)}</ul> : null}
      {number(args, 'pathsTruncated') ? <p className="tool-note">{number(args, 'pathsTruncated')} more paths not shown.</p> : null}
      {tool.preview ? <div className="edit-status">{tool.preview}</div> : null}
      {!paths.length && !tool.preview ? <p className="tool-empty">Waiting for result…</p> : null}
    </div>
  );
}

function filesSummary(args: ToolArgs, fallback: string): string {
  const paths = strings(args, 'paths');
  if (!paths.length) return fallback;
  if (paths.length === 1) return paths[0] ?? fallback;
  return `${paths.length}${number(args, 'pathsTruncated') ? '+' : ''} paths · ${paths.slice(0, 2).join(', ')}`;
}

const renderers: Record<string, ToolRenderer> = {
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

const stateLabel: Record<ToolActivity['state'], string> = {
  pending: 'Pending', awaiting_confirmation: 'Waiting', running: 'Running', success: 'Done', error: 'Failed', rejected: 'Rejected',
};
const stateMark: Record<ToolActivity['state'], string> = {
  pending: '·', awaiting_confirmation: '!', running: '·', success: '✓', error: '×', rejected: '×',
};

function fallbackTitle(name: string): string {
  return name.split(/[_-]+/).filter(Boolean).map((part) => part[0]?.toUpperCase() + part.slice(1)).join(' ') || 'Tool';
}

export function ToolCard({ tool }: { tool: ToolActivity }) {
  const args = tool.arguments ?? {};
  const renderer = renderers[tool.name];
  const title = tool.displayName || renderer?.title || fallbackTitle(tool.name);
  const summary = renderer?.summary(args, tool.argsSummary) || tool.argsSummary;
  const tone = tool.state === 'error' ? 'error' : tool.state === 'rejected' ? 'rejected' : tool.state === 'success' ? 'ok' : 'running';
  const body = renderer?.body(tool, args) ?? <PlainOutput tool={tool} />;

  return (
    <div className="tool-with-images">
      <details className={`tool tool-${tone} tool-kind-${tool.name}`} aria-label={`tool ${tool.name}`}>
        <summary>
        <span className="tool-heading">
          <span className="tool-title-row">
            <span className="tool-name">{clip(title, 80)}</span>
            {title !== tool.name ? <code className="tool-technical-name">{clip(tool.name, 60)}</code> : null}
          </span>
          {summary ? <span className="tool-args" title={summary}>{clip(summary, 300)}</span> : null}
        </span>
        <span className="tool-state"><span aria-hidden="true">{stateMark[tool.state]}</span>{stateLabel[tool.state]}</span>
        <span className="tool-chevron" aria-hidden="true">›</span>
        </summary>
        <div className="tool-body">{body}</div>
        {tool.truncated ? <p className="tool-note">Output truncated for display.</p> : null}
      </details>
      <ToolImages tool={tool} />
    </div>
  );
}

export const defaultToolRendererNames = Object.freeze(Object.keys(renderers));
