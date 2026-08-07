import * as React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { Markdown } from './Markdown';
import { Mermaid } from './Mermaid';
import { ChatHeader } from './components/ChatHeader';
import { Composer } from './components/Composer';
import { Conversation } from './components/Conversation';
import { ElicitationDialog, ToolConfirmDialog } from './components/Dialogs';
import { ModelPicker } from './components/ModelPicker';
import { PendingDialogs } from './components/PendingDialogs';
import { PluginChat } from './components/PluginChat';
import { ToolCard } from './components/ToolActivity';
import { useChat } from './useChat';
import { useDraft } from './useDraft';

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
