import { Alert, AlertDescription } from '@/components/ui/alert';
import type { Notice } from '@/protocol.gen';
import { clip } from '@/safety';

export function NoticeCard({ notice }: { notice: Notice }) {
  return (
    <Alert className={`notice notice-${notice.level}`} variant={notice.level === 'error' ? 'destructive' : 'default'}>
      <AlertDescription>{clip(notice.message, 600)}</AlertDescription>
    </Alert>
  );
}
