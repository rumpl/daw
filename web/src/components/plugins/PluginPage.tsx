import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { useEffect, useMemo, useRef, useState, type RefObject } from 'react';
import { useNavigate } from 'react-router-dom';
import { api } from '@/api';
import { createPluginUI, type PluginUI } from '@/plugin-sdk';
import type { Bootstrap, Plugin, PluginPage as PluginPageDescriptor, Workspace } from '@/protocol.gen';
import { pluginRoute } from '@/routes';
import { clip } from '@/safety';

interface PluginPageProps {
  boot: Bootstrap;
  plugin: Plugin | null;
  routePath: string;
  workspace: Workspace | null;
  menuButton: RefObject<HTMLButtonElement | null>;
  drawerOpen: boolean;
  onToggleDrawer: () => void;
}

type Cleanup = () => void | Promise<void>;

interface PluginContext {
  root: HTMLElement;
  workspace: Workspace | null;
  bootstrap: Bootstrap;
  plugin: Plugin;
  page: PluginPageDescriptor;
  routePath: string;
  signal: AbortSignal;
  api: typeof api;
  ui: PluginUI;
  navigate: (path: string) => void;
}

interface PluginModule {
  mount?: (context: PluginContext) => void | Cleanup | Promise<void | Cleanup>;
}

export function PluginPage({
  boot,
  plugin,
  routePath,
  workspace,
  menuButton,
  drawerOpen,
  onToggleDrawer,
}: PluginPageProps) {
  const navigate = useNavigate();
  const rootRef = useRef<HTMLDivElement | null>(null);
  const [status, setStatus] = useState<'loading' | 'ready' | 'error'>('loading');
  const [error, setError] = useState('');
  const normalizedPath = routePath.replace(/^\/+|\/+$/g, '');
  const page = useMemo(
    () => plugin?.pages?.find((candidate) => candidate.path === normalizedPath) ?? null,
    [normalizedPath, plugin],
  );

  useEffect(() => {
    const root = rootRef.current;
    if (!root || !plugin || !page || !plugin.entryUrl) return;

    let disposed = false;
    let cleanup: Cleanup | undefined;
    const controller = new AbortController();
    root.replaceChildren();
    const pluginUI = createPluginUI(root);
    setStatus('loading');
    setError('');

    void import(/* @vite-ignore */ plugin.entryUrl)
      .then(async (module: PluginModule) => {
        if (typeof module.mount !== 'function') {
          throw new Error('the plugin entry must export mount(context)');
        }
        const result = await module.mount({
          root,
          workspace,
          bootstrap: boot,
          plugin,
          page,
          routePath: normalizedPath,
          signal: controller.signal,
          api,
          ui: pluginUI.ui,
          navigate: (path: string) => navigate(pluginRoute(plugin.id, path)),
        });
        if (typeof result === 'function') cleanup = result;
        if (disposed) {
          await cleanup?.();
          pluginUI.cleanup();
          return;
        }
        setStatus('ready');
      })
      .catch((cause: unknown) => {
        if (disposed) return;
        setError(cause instanceof Error ? cause.message : 'the plugin could not be loaded');
        setStatus('error');
      });

    return () => {
      disposed = true;
      controller.abort();
      void cleanup?.();
      pluginUI.cleanup();
      root.replaceChildren();
    };
  }, [boot, navigate, normalizedPath, page, plugin, workspace]);

  const title = page?.label ?? plugin?.name ?? 'Plugin';
  return (
    <>
      <header className="topbar plugin-topbar">
        <Button
          ref={menuButton}
          type="button"
          variant="secondary"
          className="menu-button"
          aria-expanded={drawerOpen}
          aria-controls="sidebar"
          onClick={onToggleDrawer}
        >
          Menu
        </Button>
        <div className="topbar-title">
          <h1>{clip(title, 100)}</h1>
          {plugin?.version ? <span className="plugin-version">v{clip(plugin.version, 30)}</span> : null}
        </div>
      </header>

      {!plugin ? (
        <div className="plugin-message">
          <h2>Plugin not found</h2>
          <p>It may have been removed or its manifest may be invalid.</p>
        </div>
      ) : !page ? (
        <div className="plugin-message">
          <h2>Plugin page not found</h2>
          <p>This route is not declared by the plugin.</p>
        </div>
      ) : (
        <div className={`plugin-host plugin-${plugin.id}`}>
          {status === 'loading' ? <p className="plugin-status">Loading {clip(plugin.name, 80)}…</p> : null}
          {status === 'error' ? (
            <Alert className="banner banner-error rounded-none border-x-0 border-t-0" variant="destructive">
              <AlertDescription>{clip(error, 300)}</AlertDescription>
            </Alert>
          ) : null}
          <div ref={rootRef} className="plugin-root" data-plugin-id={plugin.id} />
        </div>
      )}
    </>
  );
}
