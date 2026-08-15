export type TreeNode = { name?: unknown; type?: unknown; children?: unknown };

export function isTreeNode(value: unknown): value is TreeNode {
  return typeof value === 'object' && value !== null && typeof (value as TreeNode).name === 'string';
}

export function TreeBranch({ node, root = false }: { node: TreeNode; root?: boolean }) {
  const children = Array.isArray(node.children) ? node.children.filter(isTreeNode) : [];
  const directory = node.type === 'directory';

  return (
    <li className={root ? 'tree-root' : undefined}>
      <span className={`tree-entry tree-${directory ? 'dir' : 'file'}`}>
        <span aria-hidden="true">{directory ? '▾' : '·'}</span>{String(node.name)}
      </span>
      {children.length ? <ul>{children.map((child, index) => <TreeBranch key={`${String(child.name)}-${index}`} node={child} />)}</ul> : null}
    </li>
  );
}
