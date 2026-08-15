import { Button } from '@/components/ui/button';
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { Edit, Menu } from 'lucide-react';
import { useState, type RefObject } from 'react';
import type { ChatState } from '@/reducer';
import { clip } from '@/safety';

interface ChatHeaderProps {
  hasChat: boolean;
  state: ChatState;
  busyAction: boolean;
  menuButton: RefObject<HTMLButtonElement | null>;
  drawerOpen: boolean;
  onToggleDrawer: () => void;
  onRename: (title: string) => void;
  showMenu?: boolean;
}

export function ChatHeader({ hasChat, state, busyAction, menuButton, drawerOpen, onToggleDrawer, onRename, showMenu = true }: ChatHeaderProps) {
  const [renameOpen, setRenameOpen] = useState(false);
  const [title, setTitle] = useState('');
  const beginRename = () => {
    setTitle(state.meta?.title ?? '');
    setRenameOpen(true);
  };

  return (
    <>
      <header className="topbar">
        {showMenu ? (
          <Button type="button" variant="secondary" className="menu-button" ref={menuButton}
            aria-expanded={drawerOpen} aria-controls="sidebar" onClick={onToggleDrawer}>
            <Menu aria-hidden="true" /> Menu
          </Button>
        ) : null}

        <div className="topbar-title">
          <h1>{clip(state.meta?.title || 'docker-agent', 80)}</h1>
          {hasChat && state.meta ? (
            <Tooltip>
              <TooltipTrigger render={
                <Button type="button" size="icon-xs" variant="ghost" className="rename-session"
                  aria-label="Rename session" onClick={beginRename} disabled={busyAction}>
                  <Edit aria-hidden="true" />
                </Button>
              } />
              <TooltipContent>Rename session</TooltipContent>
            </Tooltip>
          ) : null}
        </div>
      </header>

      <Dialog open={renameOpen} onOpenChange={setRenameOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogTitle>Rename session</DialogTitle>
          <DialogDescription>Choose a concise title for this session.</DialogDescription>
          <form className="flex flex-col gap-4" onSubmit={(event) => {
            event.preventDefault();
            const nextTitle = title.trim();
            if (!nextTitle) return;
            onRename(nextTitle);
            setRenameOpen(false);
          }}>
            <Input aria-label="Session title" value={title} onChange={(event) => setTitle(event.target.value)} autoFocus />
            <div className="flex justify-end gap-2">
              <DialogClose render={<Button type="button" variant="secondary" />}>Cancel</DialogClose>
              <Button type="submit" disabled={!title.trim()}>Rename</Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>
    </>
  );
}
