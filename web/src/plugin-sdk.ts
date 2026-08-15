import * as React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { ChatHeader } from '@/components/chat/ChatHeader';
import { Composer } from '@/components/chat/Composer';
import { ModelPicker } from '@/components/chat/ModelPicker';
import { Conversation } from '@/components/conversation/Conversation';
import { ElicitationDialog } from '@/components/dialogs/ElicitationDialog';
import { PendingDialogs } from '@/components/dialogs/PendingDialogs';
import { ToolConfirmDialog } from '@/components/dialogs/ToolConfirmDialog';
import { Markdown } from '@/components/markdown/Markdown';
import { Mermaid } from '@/components/markdown/Mermaid';
import { PluginChat } from '@/components/plugins/PluginChat';
import { ToolCard } from '@/components/tools/ToolCard';
import { useChat } from '@/hooks/useChat';
import { useDraft } from '@/hooks/useDraft';

export const pluginComponents = Object.freeze({
  Markdown,
  Mermaid,
  Chat: PluginChat,
  ChatHeader,
  Composer,
  Conversation,
  ElicitationDialog,
  ToolConfirmDialog,
  ModelPicker,
  PendingDialogs,
  ToolCard,
});

export const pluginHooks = Object.freeze({ useChat, useDraft });

export interface PluginUI {
  React: typeof React;
  components: typeof pluginComponents;
  hooks: typeof pluginHooks;
  render: (element: React.ReactNode, target?: HTMLElement) => () => void;
}

// createPluginUI gives a plugin the host's React instance and component
// registry. Every nested root is tracked and unmounted with the plugin even if
// plugin cleanup throws or forgets to release it.
export function createPluginUI(defaultTarget: HTMLElement): { ui: PluginUI; cleanup: () => void } {
  const roots = new Map<HTMLElement, Root>();

  const unmount = (target: HTMLElement) => {
    const root = roots.get(target);
    if (!root) return;
    roots.delete(target);
    root.unmount();
  };

  const ui: PluginUI = {
    React,
    components: pluginComponents,
    hooks: pluginHooks,
    render(element, target = defaultTarget) {
      unmount(target);
      const root = createRoot(target);
      roots.set(target, root);
      root.render(element);
      return () => unmount(target);
    },
  };

  return {
    ui,
    cleanup: () => {
      for (const root of roots.values()) root.unmount();
      roots.clear();
    },
  };
}
