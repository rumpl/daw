import type { ReactNode } from 'react';

/** Defers contribution rendering until inside the nearest plugin error boundary. */
export function PluginRenderContent({ render }: { render: () => ReactNode }) {
  return <>{render()}</>;
}
