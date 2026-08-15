import type { ToolActivity } from '@/protocol.gen';
import { PlainOutput } from './PlainOutput';
import { isTreeNode, TreeBranch } from './TreeBranch';

export function DirectoryTreeBody({ tool }: { tool: ToolActivity }) {
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
