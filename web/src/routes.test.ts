import { describe, expect, it } from 'vitest';
import { pluginRoute, sessionRoute } from './routes';

describe('pluginRoute', () => {
  it('encodes plugin and page path components', () => {
    expect(pluginRoute('my plugin', 'details/one two')).toBe('/plugins/my%20plugin/details/one%20two');
  });
});

describe('sessionRoute', () => {
  it('keeps the session in the path and safely encodes the workspace', () => {
    const route = sessionRoute('session/with spaces', '/Users/me/a project');
    const url = new URL(route, 'http://localhost');

    expect(url.pathname).toBe('/sessions/session%2Fwith%20spaces');
    expect(url.searchParams.get('workspace')).toBe('/Users/me/a project');
  });
});
