import { ProjectSettings } from '@/components/settings/ProjectSettings';
import { render, screen } from '@/test-utils';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

describe('ProjectSettings', () => {
  it('adds and removes projects', async () => {
    const onAddProject = vi.fn();
    const onRemoveProject = vi.fn();
    const user = userEvent.setup();

    render(<ProjectSettings projects={['/code/alpha']} workspacePath="/code/new-project"
      onWorkspacePathChange={() => undefined} onAddProject={onAddProject} onRemoveProject={onRemoveProject} />);

    await user.click(screen.getByRole('button', { name: 'Add project' }));
    await user.click(screen.getByRole('button', { name: 'Add project' }));
    expect(onAddProject).toHaveBeenCalledWith('/code/new-project');

    await user.click(screen.getByRole('button', { name: 'Remove project /code/alpha' }));
    expect(onRemoveProject).toHaveBeenCalledWith('/code/alpha');
  });
});
