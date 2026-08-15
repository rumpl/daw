import { TooltipProvider } from '@/components/ui/tooltip';
import { render as testingLibraryRender, type RenderOptions } from '@testing-library/react';
import type { ReactNode, ReactElement } from 'react';

function TestTheme({ children }: { children: ReactNode }) {
  return <TooltipProvider>{children}</TooltipProvider>;
}

export function render(ui: ReactElement, options?: Omit<RenderOptions, 'wrapper'>) {
  return testingLibraryRender(ui, { wrapper: TestTheme, ...options });
}

export * from '@testing-library/react';
