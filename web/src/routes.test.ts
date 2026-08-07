import { describe, expect, it } from 'vitest';
import { sessionRoute } from './routes';

describe('sessionRoute', () => {
  it('keeps the session in the path and safely encodes the workspace', () => {
    const route = sessionRoute('session/with spaces', '/Users/me/a project');
    const url = new URL(route, 'http://localhost');

    expect(url.pathname).toBe('/sessions/session%2Fwith%20spaces');
    expect(url.searchParams.get('workspace')).toBe('/Users/me/a project');
  });
});
