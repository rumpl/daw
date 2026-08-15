import { Alert, AlertDescription } from '@/components/ui/alert';
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from '@/components/ui/alert-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { useCallback, useEffect, useState, type RefObject } from 'react';
import { api, type PluginManagement } from '@/api';
import type { Bootstrap } from '@/protocol.gen';
import { clip } from '@/safety';

interface PluginSettingsPageProps {
  boot: Bootstrap;
  revision: number;
  menuButton: RefObject<HTMLButtonElement | null>;
  drawerOpen: boolean;
  onToggleDrawer: () => void;
}

type PluginAction = 'start' | 'stop' | 'enable' | 'disable';

export function PluginSettingsPage({ boot, revision, menuButton, drawerOpen, onToggleDrawer }: PluginSettingsPageProps) {
  const [plugins, setPlugins] = useState<PluginManagement[]>([]);
  const [catalogErrors, setCatalogErrors] = useState<Array<{ pluginId?: string; message: string }>>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [busyPlugin, setBusyPlugin] = useState<string | null>(null);
  const [pending, setPending] = useState<{ managed: PluginManagement; action: 'stop' | 'disable' | 'delete' } | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const catalog = await api.pluginManagement();
      setPlugins(catalog.plugins ?? []);
      setCatalogErrors(catalog.errors ?? []);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'The plugins could not be loaded.');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load, revision]);

  const act = async (managed: PluginManagement, action: PluginAction) => {
    setBusyPlugin(managed.plugin.id);
    setError('');
    try {
      const updated = await api.managePlugin(managed.plugin.id, action);
      setPlugins((current) => current.map((item) => item.plugin.id === managed.plugin.id ? updated : item));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : `The plugin could not be ${action}ed.`);
    } finally {
      setBusyPlugin(null);
    }
  };

  const remove = async (managed: PluginManagement) => {
    setBusyPlugin(managed.plugin.id);
    setError('');
    try {
      await api.deletePlugin(managed.plugin.id);
      setPlugins((current) => current.filter((item) => item.plugin.id !== managed.plugin.id));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'The plugin could not be deleted.');
    } finally {
      setBusyPlugin(null);
    }
  };

  return (
    <section className="main-pane">
      <header className="topbar">
        <Button ref={menuButton} type="button" variant="secondary" className="menu-button" aria-expanded={drawerOpen}
          aria-controls="sidebar" onClick={onToggleDrawer}>Menu</Button>
        <div className="topbar-title"><h1>Settings · Plugins</h1></div>
      </header>
      <div className="plugin-settings">
        <div className="plugin-settings-heading">
          <div><h2>Plugins</h2><p>Manage plugins installed in <code>{clip(boot.pluginDir, 160)}</code>.</p></div>
        </div>
        {error ? <Alert variant="destructive"><AlertDescription>{clip(error, 300)}</AlertDescription></Alert> : null}
        {catalogErrors.length > 0 ? (
          <section className="plugin-management-errors" aria-label="Invalid plugins">
            <h3>Invalid plugins</h3>
            <ul>{catalogErrors.map((item, index) => <li key={`${item.pluginId ?? 'plugin'}-${index}`}><strong>{item.pluginId ?? 'Unknown plugin'}:</strong> {clip(item.message, 300)}</li>)}</ul>
          </section>
        ) : null}
        {loading ? <p className="hint">Loading plugins…</p> : plugins.length === 0 ? <p className="hint">No plugins are installed.</p> : (
          <ul className="plugin-management-list">
            {plugins.map((managed) => {
              const { plugin } = managed;
              const features = plugin.features ?? {
                frontend: Boolean(plugin.entryUrl), styles: Boolean(plugin.styleUrl), backend: Boolean(plugin.backendUrl),
                configuration: plugin.configuration !== undefined, webhooks: [], mcpServers: [],
              };
              const webhooks = features.webhooks ?? [];
              const mcpServers = features.mcpServers ?? [];
              const busy = busyPlugin === plugin.id;
              return (
                <li key={plugin.id}>
                  <div className="plugin-management-info">
                    <div className="plugin-management-title"><h3>{clip(plugin.name || plugin.id, 100)}</h3>{plugin.version ? <span>v{clip(plugin.version, 30)}</span> : null}</div>
                    <p>{clip(plugin.description || plugin.id, 240)}</p>
                    <div className="plugin-management-status">
                      <Badge variant={managed.enabled ? 'default' : 'secondary'}>{managed.enabled ? 'Enabled' : 'Disabled'}</Badge>
                      <Badge variant={managed.running ? 'default' : 'secondary'}>{managed.running ? 'Running' : 'Stopped'}</Badge>
                      <code>{clip(plugin.id, 80)}</code>
                    </div>
                    <div className="plugin-feature-summary" aria-label="Plugin features">
                      <strong>Features</strong>
                      <div>
                        {features.frontend ? <span>Frontend contributions</span> : null}
                        {(plugin.pages ?? []).length > 0 ? <span>{plugin.pages?.length} page{plugin.pages?.length === 1 ? '' : 's'}</span> : null}
                        {features.styles ? <span>Custom styles</span> : null}
                        {features.backend ? <span>Backend API & events</span> : null}
                        {features.configuration ? <span>Configuration</span> : null}
                        {webhooks.length > 0 ? <span>{webhooks.length} webhook{webhooks.length === 1 ? '' : 's'}</span> : null}
                        {mcpServers.length > 0 ? <span>{mcpServers.length} MCP server{mcpServers.length === 1 ? '' : 's'}</span> : null}
                      </div>
                      {(plugin.pages ?? []).length > 0 ? <p><b>Pages:</b> {plugin.pages?.map((page) => page.label).join(', ')}</p> : null}
                      {mcpServers.length > 0 ? <p><b>MCP:</b> {mcpServers.map((server) => `${server.id} (${server.transport})`).join(', ')}</p> : null}
                      {webhooks.length > 0 ? <p><b>Webhooks:</b> {webhooks.join(', ')}</p> : null}
                    </div>
                  </div>
                  <div className="plugin-management-actions">
                    {managed.enabled ? <Button type="button" variant="secondary" onClick={() => setPending({ managed, action: 'disable' })} disabled={busy}>Disable</Button> : <Button type="button" onClick={() => void act(managed, 'enable')} disabled={busy}>Enable</Button>}
                    {managed.running ? <Button type="button" variant="secondary" onClick={() => setPending({ managed, action: 'stop' })} disabled={busy}>Stop</Button> : <Button type="button" onClick={() => void act(managed, 'start')} disabled={busy || !managed.enabled}>Start</Button>}
                    <Button type="button" variant="destructive" onClick={() => setPending({ managed, action: 'delete' })} disabled={busy}>Delete</Button>
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </div>

      <AlertDialog open={Boolean(pending)} onOpenChange={(open) => { if (!open) setPending(null); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {pending?.action === 'delete' ? 'Delete' : pending?.action === 'disable' ? 'Disable' : 'Stop'}{' '}
              {pending?.managed.plugin.name || pending?.managed.plugin.id}?
            </AlertDialogTitle>
            <AlertDialogDescription>
              {pending?.action === 'delete'
                ? 'This removes the plugin from disk and cannot be undone.'
                : pending?.action === 'disable'
                  ? 'The plugin will no longer load or contribute UI and backend features.'
                  : 'The plugin backend will be stopped.'}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction variant={pending?.action === 'delete' ? 'destructive' : 'default'} onClick={() => {
              if (!pending) return;
              const current = pending;
              setPending(null);
              if (current.action === 'delete') void remove(current.managed);
              else void act(current.managed, current.action);
            }}>
              {pending?.action === 'delete' ? 'Delete' : pending?.action === 'disable' ? 'Disable' : 'Stop'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}
