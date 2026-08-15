import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { ModelPicker } from '@/components/chat/ModelPicker';
import { ToolPicker } from '@/components/chat/ToolPicker';
import { api, CHAT_OPTIONS_CHANGE_EVENT } from '@/api';
import { loadPrefs, updateThemePreference, type ThemeMode } from '@/preferences';
import { THEME_CHANGE_EVENT } from '@/components/shell/AppTheme';
import type { ChatOptions } from '@/protocol.gen';
import { useEffect, useState, type RefObject } from 'react';

interface SettingsPageProps {
  menuButton: RefObject<HTMLButtonElement | null>;
  drawerOpen: boolean;
  onToggleDrawer: () => void;
  onOpenPlugins: () => void;
}

export function SettingsPage({ menuButton, drawerOpen, onToggleDrawer, onOpenPlugins }: SettingsPageProps) {
  const [options, setOptions] = useState<ChatOptions | null>(null);
  const [theme, setTheme] = useState<ThemeMode>(() => loadPrefs().theme);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    void api.chatOptions().then(setOptions).catch((cause: unknown) =>
      setError(cause instanceof Error ? cause.message : 'Settings could not be loaded.'),
    );
  }, []);

  const updateChat = async (patch: { model?: string; thinkingLevel?: string }) => {
    setBusy(true);
    setError('');
    try {
      setOptions(await api.updateChatOptions(patch));
      window.dispatchEvent(new Event(CHAT_OPTIONS_CHANGE_EVENT));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'The default chat settings could not be saved.');
    } finally {
      setBusy(false);
    }
  };

  const updateTool = async (name: string, enabled: boolean) => {
    if (!options) return;
    const previous = options;
    setOptions({ ...options, tools: (options.tools ?? []).map((tool) => tool.name === name ? { ...tool, enabled } : tool) });
    setError('');
    try {
      const updated = await api.updateDefaultTool(name, enabled);
      setOptions((current) => current ? {
        ...current,
        tools: (current.tools ?? []).map((tool) => tool.name === updated.name ? updated : tool),
      } : current);
      window.dispatchEvent(new Event(CHAT_OPTIONS_CHANGE_EVENT));
    } catch (cause) {
      setOptions(previous);
      setError(cause instanceof Error ? cause.message : 'The tool setting could not be saved.');
    }
  };

  const updateTheme = (next: ThemeMode) => {
    try {
      updateThemePreference(next);
    } catch {
      setError('The appearance setting could not be saved in this browser.');
      return;
    }
    setTheme(next);
    window.dispatchEvent(new Event(THEME_CHANGE_EVENT));
  };

  return (
    <section className="main-pane">
      <header className="topbar">
        <Button ref={menuButton} type="button" variant="secondary" className="menu-button" aria-expanded={drawerOpen}
          aria-controls="sidebar" onClick={onToggleDrawer}>Menu</Button>
        <div className="topbar-title"><h1>Settings</h1></div>
      </header>
      <div className="settings-page">
        <div className="plugin-settings-heading"><div><h2>Settings</h2><p>Configure the dashboard and defaults inherited by new chats.</p></div></div>
        {error ? <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert> : null}

        <section className="settings-section" aria-labelledby="appearance-settings">
          <div><h3 id="appearance-settings">Appearance</h3><p>Choose how the dashboard is displayed in this browser.</p></div>
          <Select value={theme} onValueChange={(value) => updateTheme(value as ThemeMode)}>
            <SelectTrigger aria-label="Frontend mode"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="light">Light</SelectItem>
              <SelectItem value="dark">Dark</SelectItem>
              <SelectItem value="system">System</SelectItem>
            </SelectContent>
          </Select>
        </section>

        <section className="settings-section" aria-labelledby="model-settings">
          <div><h3 id="model-settings">Default model</h3><p>Used when creating a new chat. Existing chats keep their own model.</p></div>
          {options ? <div className="settings-controls">
            <ModelPicker models={options.models ?? []} current={options.model} disabled={busy || !(options.models?.length)}
              onSelect={(model) => void updateChat({ model })} />
            <Select value={options.thinkingLevel || '__unavailable'} disabled={busy || !(options.thinkingLevels?.length)}
              onValueChange={(thinkingLevel) => { if (thinkingLevel) void updateChat({ thinkingLevel }); }}>
              <SelectTrigger aria-label="Default thinking effort"><SelectValue placeholder="Thinking effort" /></SelectTrigger>
              <SelectContent>{(options.thinkingLevels ?? []).map((level) => <SelectItem key={level} value={level}>{level}</SelectItem>)}</SelectContent>
            </Select>
          </div> : <p className="hint">Loading model options…</p>}
        </section>

        <section className="settings-section" aria-labelledby="tool-settings">
          <div><h3 id="tool-settings">Default tools</h3><p>Disabled tools are not offered to the model in chats opened or resumed after this change.</p></div>
          {options ? <ToolPicker tools={options.tools ?? []} disabled={false}
            onChange={(name, enabled) => void updateTool(name, enabled)}
            onRefresh={async () => setOptions(await api.chatOptions())} /> : <p className="hint">Loading tools…</p>}
        </section>
        <section className="settings-section" aria-labelledby="plugin-settings">
          <div><h3 id="plugin-settings">Plugins</h3><p>Manage installed plugins, backend processes, and contributed features.</p></div>
          <Button type="button" variant="secondary" onClick={onOpenPlugins}>Manage plugins</Button>
        </section>
      </div>
    </section>
  );
}
