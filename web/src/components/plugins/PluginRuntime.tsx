import { useEffect } from 'react';
import { api } from '@/api';
import { createContributionRegistry, removePluginContributions } from '@/plugin-contributions';
import { createPluginEvents } from '@/plugin-events';
import { createPluginUI } from '@/plugin-sdk';
import type { Bootstrap, Plugin, Workspace } from '@/protocol.gen';

interface ActivationContext {
  bootstrap: Bootstrap;
  plugin: Plugin;
  workspace: Workspace | null;
  signal: AbortSignal;
  api: typeof api;
  ui: ReturnType<typeof createPluginUI>['ui'];
  events: ReturnType<typeof createPluginEvents>;
  contributions: ReturnType<typeof createContributionRegistry>;
}

type Cleanup = () => void | Promise<void>;
interface PluginModule {
  activate?: (context: ActivationContext) => void | Cleanup | Promise<void | Cleanup>;
}

export function PluginRuntime({ boot, plugins, workspace }: {
  boot: Bootstrap;
  plugins: Plugin[];
  workspace: Workspace | null;
}) {
  useEffect(() => {
    const runtimes = plugins.map((plugin) => {
      const controller = new AbortController();
      const detachedRoot = document.createElement('div');
      const pluginUI = createPluginUI(detachedRoot);
      const stylesheet = plugin.styleUrl ? document.createElement('link') : null;
      if (stylesheet && plugin.styleUrl) {
        stylesheet.rel = 'stylesheet';
        stylesheet.href = plugin.styleUrl;
        stylesheet.dataset.dawPlugin = plugin.id;
        document.head.append(stylesheet);
      }
      const contributions = createContributionRegistry(plugin.id);
      const events = createPluginEvents(controller.signal);
      let cleanup: Cleanup | undefined;
      let disposed = false;

      if (!plugin.entryUrl) {
        return async () => {
          controller.abort();
          removePluginContributions(plugin.id);
          events.close();
          pluginUI.cleanup();
          stylesheet?.remove();
        };
      }
      void import(/* @vite-ignore */ plugin.entryUrl).then(async (module: PluginModule) => {
        if (typeof module.activate !== 'function') return;
        const result = await module.activate({
          bootstrap: boot,
          plugin,
          workspace,
          signal: controller.signal,
          api,
          ui: pluginUI.ui,
          events,
          contributions,
        });
        if (typeof result === 'function') cleanup = result;
        if (disposed) await cleanup?.();
      }).catch((cause: unknown) => {
        if (disposed) return;
        contributions.notify({
          id: 'activation-error',
          level: 'error',
          title: `${plugin.name} could not start`,
          message: cause instanceof Error ? cause.message : 'Plugin activation failed',
          timeoutMs: 0,
        });
      });

      return async () => {
        disposed = true;
        controller.abort();
        removePluginContributions(plugin.id);
        await cleanup?.();
        events.close();
        pluginUI.cleanup();
        stylesheet?.remove();
      };
    });

    return () => {
      for (const dispose of runtimes) void dispose();
    };
  }, [boot, plugins, workspace]);

  return null;
}
