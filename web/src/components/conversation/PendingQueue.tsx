import type { QueueStatus } from '@/protocol.gen';

export function PendingQueue({ queue }: { queue: QueueStatus }) {
  if ((queue.steer?.length ?? 0) === 0 && (queue.followUps?.length ?? 0) === 0) return null;

  return (
    <section className="pending-queue" aria-label="Pending messages">
      <header>Pending</header>
      {(queue.steer ?? []).map((message) => (
        <article className="queued-message" key={`steer:${message.id}`}>
          <span>Steer</span>
          <pre>{message.text}</pre>
        </article>
      ))}
      {(queue.followUps ?? []).map((message) => (
        <article className="queued-message" key={`followUp:${message.id}`}>
          <span>Follow-up</span>
          <pre>{message.text}</pre>
        </article>
      ))}
    </section>
  );
}
