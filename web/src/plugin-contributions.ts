import type { ReactNode } from 'react';
import { useSyncExternalStore } from 'react';
import type { SessionMeta, Workspace } from './protocol.gen';

export type PluginSlot = 'composer.actions' | 'session-tab.badge' | 'sidebar.footer';
export type PluginActionLocation = 'command-palette' | 'composer';
export type PluginNotificationLevel = 'info' | 'warning' | 'error';

export interface ContributionContext {
  workspace: Workspace | null;
  chatId: string | null;
  session: SessionMeta | null;
  sessionId?: string;
}

export interface PluginAction {
  id: string;
  label: string;
  description?: string;
  locations: PluginActionLocation[];
  when?: (context: ContributionContext) => boolean;
  run(context: ContributionContext): void | Promise<void>;
}

export interface SlotContribution {
  id: string;
  slot: PluginSlot;
  order?: number;
  render(context: ContributionContext): ReactNode;
}

export interface PluginCommand {
  id: string;
  name: string;
  description: string;
  run(text: string, context: ContributionContext): string | void | Promise<string | void>;
}

export interface ToolRendererContribution {
  id: string;
  match(tool: import('./protocol.gen').ToolActivity): boolean;
  render(tool: import('./protocol.gen').ToolActivity): ReactNode;
}

export interface AttachmentRendererContribution {
  id: string;
  match(attachment: import('./protocol.gen').Attachment): boolean;
  render(attachment: import('./protocol.gen').Attachment): ReactNode;
}

export interface PluginNotification {
  id: string;
  level: PluginNotificationLevel;
  title: string;
  message?: string;
  timeoutMs?: number;
}

interface Owned<T> { pluginId: string; value: T }
interface NotificationRecord extends PluginNotification { pluginId: string; key: string }

let revision = 0;
const listeners = new Set<() => void>();
const actions = new Map<string, Owned<PluginAction>>();
const slots = new Map<string, Owned<SlotContribution>>();
const badges = new Map<string, Owned<{ sessionId: string; value: string; tone: PluginNotificationLevel | 'success' }>>();
const notifications = new Map<string, NotificationRecord>();
const commands = new Map<string, Owned<PluginCommand>>();
const toolRenderers = new Map<string, Owned<ToolRendererContribution>>();
const attachmentRenderers = new Map<string, Owned<AttachmentRendererContribution>>();
const notificationTimers = new Map<string, number>();

function emit() {
  revision += 1;
  for (const listener of listeners) listener();
}

function ownedKey(pluginId: string, id: string) {
  return `${pluginId}:${id}`;
}

function register<T>(store: Map<string, Owned<T>>, pluginId: string, value: T & { id: string }) {
  const key = ownedKey(pluginId, value.id);
  store.set(key, { pluginId, value });
  emit();
  return () => {
    if (store.delete(key)) emit();
  };
}

export function createContributionRegistry(pluginId: string) {
  return Object.freeze({
    registerAction(action: PluginAction) {
      return register(actions, pluginId, action);
    },
    registerSlot(contribution: SlotContribution) {
      return register(slots, pluginId, contribution);
    },
    registerCommand(command: PluginCommand) {
      return register(commands, pluginId, command);
    },
    registerToolRenderer(renderer: ToolRendererContribution) {
      return register(toolRenderers, pluginId, renderer);
    },
    registerAttachmentRenderer(renderer: AttachmentRendererContribution) {
      return register(attachmentRenderers, pluginId, renderer);
    },
    setSessionBadge(sessionId: string, badge: { id: string; value: string; tone?: PluginNotificationLevel | 'success' }) {
      const key = ownedKey(pluginId, badge.id);
      badges.set(key, { pluginId, value: { sessionId, value: badge.value, tone: badge.tone ?? 'info' } });
      emit();
      return () => {
        if (badges.delete(key)) emit();
      };
    },
    notify(notification: PluginNotification) {
      const key = ownedKey(pluginId, notification.id);
      const timer = notificationTimers.get(key);
      if (timer !== undefined) window.clearTimeout(timer);
      notifications.set(key, { ...notification, pluginId, key });
      if (notification.timeoutMs !== 0) {
        notificationTimers.set(key, window.setTimeout(() => dismissNotification(key), notification.timeoutMs ?? 6000));
      }
      emit();
      return () => dismissNotification(key);
    },
  });
}

export function removePluginContributions(pluginId: string) {
  let changed = false;
  for (const store of [actions, slots, badges, commands, toolRenderers, attachmentRenderers] as Array<Map<string, Owned<unknown>>>) {
    for (const [key, entry] of store) {
      if (entry.pluginId === pluginId) changed = store.delete(key) || changed;
    }
  }
  for (const [key, entry] of notifications) {
    if (entry.pluginId === pluginId) {
      const timer = notificationTimers.get(key);
      if (timer !== undefined) window.clearTimeout(timer);
      notificationTimers.delete(key);
      changed = notifications.delete(key) || changed;
    }
  }
  if (changed) emit();
}

export function dismissNotification(key: string) {
  const timer = notificationTimers.get(key);
  if (timer !== undefined) window.clearTimeout(timer);
  notificationTimers.delete(key);
  if (notifications.delete(key)) emit();
}

export function usePluginContributions() {
  useSyncExternalStore(
    (listener) => { listeners.add(listener); return () => listeners.delete(listener); },
    () => revision,
    () => revision,
  );
  return {
    actions: Array.from(actions, ([key, entry]) => ({...entry.value, _key: key})),
    slots: Array.from(slots, ([key, entry]) => ({...entry.value, _key: key})).sort((a, b) =>
      (a.order ?? 0) - (b.order ?? 0) || a._key.localeCompare(b._key)),
    badges: Array.from(badges, ([key, entry]) => ({...entry.value, _key: key})),
    commands: Array.from(commands, ([key, entry]) => ({...entry.value, _key: key})),
    toolRenderers: Array.from(toolRenderers, ([key, entry]) => ({...entry.value, _key: key})),
    attachmentRenderers: Array.from(attachmentRenderers, ([key, entry]) => ({...entry.value, _key: key})),
    notifications: Array.from(notifications.values()),
  };
}
