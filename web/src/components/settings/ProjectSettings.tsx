import { useState, type FormEvent } from 'react';
import { FolderPlus, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';

interface ProjectSettingsProps {
  projects: string[];
  workspacePath: string;
  onWorkspacePathChange: (path: string) => void;
  onAddProject: (path: string) => void;
  onRemoveProject: (path: string) => void;
}

export function ProjectSettings({
  projects,
  workspacePath,
  onWorkspacePathChange,
  onAddProject,
  onRemoveProject,
}: ProjectSettingsProps) {
  const [dialogOpen, setDialogOpen] = useState(false);
  const addProject = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!workspacePath.trim()) return;
    onAddProject(workspacePath);
    setDialogOpen(false);
  };

  return (
    <>
      <div className="plugin-settings-heading">
        <div><h2>Projects</h2><p>Manage the working directories shown in the sidebar.</p></div>
        <Button type="button" onClick={() => setDialogOpen(true)}><FolderPlus aria-hidden="true" /> Add project</Button>
      </div>
      <p className="settings-help">Removing a project does not delete its files or sessions.</p>
      {projects.length === 0 ? <p className="hint">No projects added.</p> : (
        <ul className="settings-project-list">
          {projects.map((path) => (
            <li key={path}>
              <span title={path}>{path}</span>
              <Button type="button" size="icon-sm" variant="ghost" aria-label={`Remove project ${path}`}
                onClick={() => onRemoveProject(path)}>
                <Trash2 aria-hidden="true" />
              </Button>
            </li>
          ))}
        </ul>
      )}

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="sm:max-w-md" aria-describedby="settings-add-project-description">
          <DialogTitle>Add a project</DialogTitle>
          <DialogDescription id="settings-add-project-description">Enter the absolute path to a working directory.</DialogDescription>
          <form onSubmit={addProject} className="flex flex-col gap-3 pt-2">
            <Input aria-label="Working directory path" value={workspacePath}
              onChange={(event) => onWorkspacePathChange(event.target.value)}
              placeholder="/absolute/path/to/project" autoFocus />
            <div className="flex justify-end gap-3">
              <DialogClose render={<Button type="button" variant="secondary" />}>Cancel</DialogClose>
              <Button type="submit" disabled={!workspacePath.trim()}>Add project</Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>
    </>
  );
}
