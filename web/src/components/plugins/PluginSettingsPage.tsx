import { Alert, AlertDescription } from '@/components/ui/alert';
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu';
import { Input } from '@/components/ui/input';
import { MoreHorizontal } from 'lucide-react';
import { useCallback, useEffect, useState, type RefObject } from 'react';
import { api, type PluginManagement } from '@/api';
import type { Bootstrap } from '@/protocol.gen';
import { SettingsLayout } from '@/components/settings/SettingsLayout';
import { SettingsHeader } from '@/components/settings/SettingsHeader';
import { clip } from '@/safety';

interface PluginSettingsPageProps {
  boot: Bootstrap;
  revision: number;
  menuButton: RefObject<HTMLButtonElement | null>;
  drawerOpen: boolean;
  onToggleDrawer: () => void;
  onClose: () => void;
}

type PluginAction = 'start' | 'stop' | 'enable' | 'disable';

export function PluginSettingsPage({ boot, revision, menuButton, drawerOpen, onToggleDrawer, onClose }: PluginSettingsPageProps) {
  const [plugins, setPlugins] = useState<PluginManagement[]>([]);
  const [catalogErrors, setCatalogErrors] = useState<Array<{ pluginId?: string; message: string }>>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [busyPlugin, setBusyPlugin] = useState<string | null>(null);
  const [reference, setReference] = useState('');
  const [installing, setInstalling] = useState(false);
  const [pushTarget, setPushTarget] = useState<PluginManagement | null>(null);
  const [pushReference, setPushReference] = useState('');
  const [pushResult, setPushResult] = useState('');
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

  const install = async () => {
    const value = reference.trim();
    if (!value) return;
    setInstalling(true);
    setError('');
    try {
      await api.installPlugin(value);
      setReference('');
      await load();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'The plugin could not be installed.');
    } finally {
      setInstalling(false);
    }
  };

  const push = async () => {
    if (!pushTarget || !pushReference.trim()) return;
    setBusyPlugin(pushTarget.plugin.id);
    setError('');
    setPushResult('');
    try {
      const result = await api.pushPlugin(pushTarget.plugin.id, pushReference.trim());
      setPushResult(result.reference);
      setPushReference('');
      setPushTarget(null);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'The plugin could not be pushed.');
    } finally {
      setBusyPlugin(null);
    }
  };

  return (
    <section className="main-pane">
      <SettingsHeader title="Settings · Plugins" menuButton={menuButton} drawerOpen={drawerOpen}
        onToggleDrawer={onToggleDrawer} onClose={onClose} />
      <div className="plugin-settings">
        <SettingsLayout>
        <div className="plugin-settings-heading">
          <div><h2>Plugins</h2><p>Manage plugins installed in <code>{clip(boot.pluginDir, 160)}</code>.</p></div>
        </div>
        <form className="flex gap-2" onSubmit={(event) => { event.preventDefault(); void install(); }}>
          <Input value={reference} onChange={(event) => setReference(event.target.value)}
            placeholder="docker.io/username/plugin:version" aria-label="Plugin OCI reference" disabled={installing} />
          <Button type="submit" disabled={installing || !reference.trim()}>{installing ? 'Installing…' : 'Install'}</Button>
        </form>
        <p className="hint">Pulls a trusted Atelier plugin OCI artifact using your Docker credentials, installs it, and enables it.</p>
        {pushResult ? <Alert><AlertDescription>Published as <code>{clip(pushResult, 240)}</code></AlertDescription></Alert> : null}
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
                    <details className="plugin-details">
                      <summary>Details</summary>
                      <dl>
                        <div><dt>ID</dt><dd><code>{clip(plugin.id, 100)}</code></dd></div>
                        <div><dt>Capabilities</dt><dd>{[
                          features.frontend && 'Frontend',
                          (plugin.pages ?? []).length > 0 && `${plugin.pages?.length} page${plugin.pages?.length === 1 ? '' : 's'}`,
                          features.styles && 'Styles',
                          features.backend && 'Backend',
                          features.configuration && 'Configuration',
                          webhooks.length > 0 && `${webhooks.length} webhook${webhooks.length === 1 ? '' : 's'}`,
                          mcpServers.length > 0 && `${mcpServers.length} MCP server${mcpServers.length === 1 ? '' : 's'}`,
                        ].filter(Boolean).join(', ') || 'None declared'}</dd></div>
                        {(plugin.pages ?? []).length > 0 ? <div><dt>Pages</dt><dd>{plugin.pages?.map((page) => page.label).join(', ')}</dd></div> : null}
                        {mcpServers.length > 0 ? <div><dt>MCP</dt><dd>{mcpServers.map((server) => `${server.id} (${server.transport})`).join(', ')}</dd></div> : null}
                      </dl>
                    </details>
                  </div>
                  <div className="plugin-management-actions">
                    {!managed.enabled ? (
                      <Button type="button" variant="secondary" onClick={() => void act(managed, 'enable')} disabled={busy}>Enable</Button>
                    ) : managed.running ? (
                      <Button type="button" variant="secondary" onClick={() => setPending({ managed, action: 'stop' })} disabled={busy}>Stop</Button>
                    ) : (
                      <Button type="button" variant="secondary" onClick={() => void act(managed, 'start')} disabled={busy}>Start</Button>
                    )}
                    <DropdownMenu>
                      <DropdownMenuTrigger render={
                        <Button type="button" size="icon-sm" variant="ghost" aria-label={`More actions for ${plugin.name || plugin.id}`} disabled={busy}>
                          <MoreHorizontal aria-hidden="true" />
                        </Button>
                      } />
                      <DropdownMenuContent align="end" className="w-36">
                        {managed.enabled ? <DropdownMenuItem onClick={() => setPending({ managed, action: 'disable' })}>Disable</DropdownMenuItem> : null}
                        <DropdownMenuItem onClick={() => { setPushTarget(managed); setPushReference(''); setPushResult(''); }}>Push to registry…</DropdownMenuItem>
                        <DropdownMenuItem variant="destructive" onClick={() => setPending({ managed, action: 'delete' })}>Delete</DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </div>
                </li>
              );
            })}
          </ul>
        )}
        </SettingsLayout>
      </div>

      <AlertDialog open={Boolean(pushTarget)} onOpenChange={(open) => { if (!open && !busyPlugin) setPushTarget(null); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Push {pushTarget?.plugin.name || pushTarget?.plugin.id} to a registry</AlertDialogTitle>
            <AlertDialogDescription>
              Packages the current local plugin and pushes it using credentials from your Docker configuration.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <form id="push-plugin-form" onSubmit={(event) => { event.preventDefault(); void push(); }}>
            <Input autoFocus value={pushReference} onChange={(event) => setPushReference(event.target.value)}
              placeholder="docker.io/username/plugin:version" aria-label="Destination OCI reference" disabled={Boolean(busyPlugin)} />
          </form>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={Boolean(busyPlugin)}>Cancel</AlertDialogCancel>
            <Button type="submit" form="push-plugin-form" disabled={Boolean(busyPlugin) || !pushReference.trim()}>
              {busyPlugin ? 'Pushing…' : 'Push'}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

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
