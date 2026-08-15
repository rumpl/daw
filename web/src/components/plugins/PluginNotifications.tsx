import { Alert, AlertAction, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { dismissNotification, usePluginContributions } from '@/plugin-contributions';
import { X } from 'lucide-react';

export function PluginNotifications() {
  const { notifications } = usePluginContributions();
  if (notifications.length === 0) return null;

  return (
    <aside className="plugin-notifications" aria-label="Plugin notifications" aria-live="polite">
      {notifications.map((notification) => (
        <Alert className={`plugin-notification plugin-notification-${notification.level}`} key={notification.key}
          variant={notification.level === 'error' ? 'destructive' : 'default'}>
          <AlertTitle>{notification.title}</AlertTitle>
          {notification.message ? <AlertDescription>{notification.message}</AlertDescription> : null}
          <AlertAction>
            <Button type="button" size="icon-xs" variant="ghost" aria-label={`Dismiss ${notification.title}`}
              onClick={() => dismissNotification(notification.key)}><X aria-hidden="true" /></Button>
          </AlertAction>
        </Alert>
      ))}
    </aside>
  );
}
