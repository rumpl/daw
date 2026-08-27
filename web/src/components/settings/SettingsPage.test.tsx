import { api } from '@/api';
import { SettingsPage } from '@/components/settings/SettingsPage';
import { render, screen, waitFor } from '@/test-utils';
import type { ChatOptions } from '@/protocol.gen';
import userEvent from '@testing-library/user-event';
import { createRef } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';

const options: ChatOptions = {
  model: 'fake/model-a',
  thinkingLevel: 'medium',
  thinkingLevels: ['medium'],
  models: [],
  tools: [],
};

describe('SettingsPage', () => {
  afterEach(() => vi.restoreAllMocks());

  it('loads and saves the Docker Agent models gateway', async () => {
    vi.spyOn(api, 'chatOptions').mockResolvedValue(options);
    vi.spyOn(api, 'modelsGateway').mockResolvedValue({ url: 'https://old.example.com/proxy' });
    const update = vi.spyOn(api, 'updateModelsGateway').mockResolvedValue({ url: 'https://gateway.example.com/proxy' });
    const user = userEvent.setup();

    render(<SettingsPage menuButton={createRef<HTMLButtonElement>()} drawerOpen={false}
      onToggleDrawer={() => undefined} onOpenPlugins={() => undefined} />);

    const input = await screen.findByLabelText('LLM gateway URL');
    expect(input).toHaveValue('https://old.example.com/proxy');
    await user.clear(input);
    await user.type(input, 'https://gateway.example.com/proxy');

    const save = screen.getByRole('button', { name: 'Save' });
    await waitFor(() => expect(save).toBeEnabled());
    await user.click(save);

    await waitFor(() => expect(update).toHaveBeenCalledWith('https://gateway.example.com/proxy'));
    await waitFor(() => expect(save).toBeDisabled());
  });
});
