import { Component, type ReactNode } from 'react';

interface PluginBoundaryProps {
  children: ReactNode;
  message?: string;
}

export class PluginBoundary extends Component<PluginBoundaryProps, { failed: boolean }> {
  override state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  override render(): ReactNode {
    if (this.state.failed) {
      return <span className="plugin-contribution-error">{this.props.message ?? 'Plugin contribution failed'}</span>;
    }
    return this.props.children;
  }
}
