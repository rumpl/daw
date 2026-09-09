import { MemoryRouter } from 'react-router-dom';
import { render, screen } from '@/test-utils';
import { describe, expect, it } from 'vitest';
import { SettingsLayout } from './SettingsLayout';

describe('SettingsLayout', () => {
  it('uses router links for settings sections', () => {
    render(<MemoryRouter initialEntries={['/settings']}><SettingsLayout><p>General content</p></SettingsLayout></MemoryRouter>);

    expect(screen.getByRole('link', { name: 'General' })).toHaveAttribute('aria-current', 'page');
    expect(screen.getByRole('link', { name: 'Projects' })).toHaveAttribute('href', '/settings/projects');
    expect(screen.getByRole('link', { name: 'Plugins' })).toHaveAttribute('href', '/settings/plugins');
  });
});
