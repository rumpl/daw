import { expect, test, type Page } from '@playwright/test';
import { mkdtempSync, writeFileSync, mkdirSync } from 'node:fs';
import { tmpdir, homedir } from 'node:os';
import { join } from 'node:path';

// A workspace inside the server's allowed root (HOME by default).
const workspaceRoot = join(homedir(), '.dawui-e2e');
mkdirSync(workspaceRoot, { recursive: true });
const workspace = mkdtempSync(join(workspaceRoot, 'ws-'));

const pluginRoot = process.env.E2E_PLUGIN_DIR ?? join(homedir(), '.dawui-e2e', 'plugins');
const pluginDir = join(pluginRoot, 'system-info');
mkdirSync(pluginDir, { recursive: true });
writeFileSync(join(pluginDir, 'plugin.json'), JSON.stringify({
  apiVersion: 1,
  id: 'system-info',
  name: 'System info',
  description: 'E2E global plugin',
  version: '1.0.0',
  entry: 'index.js',
  pages: [{ id: 'overview', path: '', label: 'System info', sidebar: true }],
}));
const pluginEntry = (title: string) => `
export async function mount(context) {
  const health = await context.api.request('GET', '/api/health', undefined, { signal: context.signal });
  const { React, components, render } = context.ui;
  return render(React.createElement(components.Markdown, null, '# ${title}\\n\\nBackend: **' + health.status + '**'));
}
`;
writeFileSync(join(pluginDir, 'index.js'), pluginEntry('Plugin API'));

// Opening a working directory is the ONLY setup step: the server falls back to
// its SDK-built coding agent, so no agent config is ever chosen here.
async function openWorkspaceAndAgent(page: Page) {
  await page.locator('.project-switcher').click();
  const picker = page.getByRole('dialog', { name: 'Choose a project' });
  const openAnother = picker.getByRole('button', { name: 'Open another directory…' });
  if (await openAnother.isVisible()) await openAnother.click();
  await picker.getByLabel('Working directory path').fill(workspace);
  await picker.getByRole('button', { name: 'Open', exact: true }).click();
  await expect(picker).toBeHidden();
  await openDrawerIfMobile(page);
  await expect(page.locator('.project-switcher')).toContainText(workspace);
}

async function openDrawerIfMobile(page: Page) {
  const menu = page.getByRole('button', { name: 'Menu' });
  if (await menu.isVisible()) await menu.click();
}

/** On mobile the chat controls live in a bottom sheet behind Settings. */
async function openControls(page: Page) {
  const controls = page.locator('#chat-controls');
  if ((page.viewportSize()?.width ?? Number.POSITIVE_INFINITY) <= 820) {
    await page.getByRole('button', { name: 'Settings' }).click();
  }
  await expect(controls).toBeVisible();
}

const composer = (page: Page) => page.getByRole('textbox', { name: 'Message' });

const newChatButton = (page: Page) => page.locator('#sidebar .new-chat-button');

async function createChat(page: Page) {
  await newChatButton(page).click();
  await expect(page.getByRole('textbox', { name: 'What would you like to work on?' })).toBeVisible();
}

async function startChat(page: Page, message: string) {
  await createChat(page);
  await page.getByRole('textbox', { name: 'What would you like to work on?' }).fill(message);
  await page.getByRole('button', { name: 'Start chat' }).click();
  await expect(composer(page)).toBeVisible();
}

test.describe('dashboard', () => {
  test('loads and hot-reloads a global plugin with host components and backend API', async ({ page }) => {
    writeFileSync(join(pluginDir, 'index.js'), pluginEntry('Plugin API'));
    await page.goto('/');
    await openDrawerIfMobile(page);
    await page.getByRole('button', { name: 'System info' }).click();
    await expect(page).toHaveURL(/\/plugins\/system-info/);
    await expect(page.getByRole('heading', { name: 'Plugin API' })).toBeVisible();
    await expect(page.getByText('Backend:')).toContainText('ok');

    writeFileSync(join(pluginDir, 'index.js'), pluginEntry('Plugin reloaded'));
    await expect(page.getByRole('heading', { name: 'Plugin reloaded' })).toBeVisible({ timeout: 10_000 });
  });

  test('a workspace alone is enough: SDK-built agent, with model and thinking controls', async ({ page }) => {
    await page.goto('/');
    await openDrawerIfMobile(page);
    await openWorkspaceAndAgent(page);

    // No agent was chosen; New chat is immediately available.
    const newChat = newChatButton(page);
    await expect(newChat).toBeEnabled();
    await createChat(page);

    // Model and thinking budget are still selectable.
    await openControls(page);
    await expect(page.getByLabel('Thinking budget')).toBeEnabled();
    await page.getByLabel('Thinking budget').selectOption('high');
    await expect(page.getByLabel('Thinking budget')).toHaveValue('high');
  });

  test('model picker: search, keyboard select, and it applies', async ({ page }) => {
    await page.goto('/');
    await openDrawerIfMobile(page);
    await openWorkspaceAndAgent(page);
    await createChat(page);
    await openControls(page);

    // The trigger names the current model rather than listing everything.
    const trigger = page.getByRole('button', { name: /^Model:/ });
    await expect(trigger).toBeEnabled();
    await trigger.click();

    const dialog = page.getByRole('dialog', { name: 'Choose a model' });
    await expect(dialog).toBeVisible();
    // Grouped: the agent's own models come first.
    await expect(dialog.getByText('From this agent')).toBeVisible();
    await expect(dialog.getByText('agent default')).toBeVisible();

    // Filtering narrows the list.
    await dialog.getByLabel('Search models').fill('sonnet');
    await expect(dialog.getByRole('option')).toHaveCount(1);
    await expect(dialog.getByText('anthropic/claude-sonnet-4-5')).toBeVisible();

    // Enter picks the highlighted row. On mobile choosing also closes the
    // settings sheet, so reopen it before reading the trigger back.
    await dialog.getByLabel('Search models').press('Enter');
    await expect(dialog).toBeHidden();
    await openControls(page);
    await expect(page.getByRole('button', { name: /^Model:/ })).toContainText('claude-sonnet-4-5');
  });

  test('model picker closes with Escape without changing the model', async ({ page }) => {
    await page.goto('/');
    await openDrawerIfMobile(page);
    await openWorkspaceAndAgent(page);
    await createChat(page);
    await openControls(page);

    const trigger = page.getByRole('button', { name: /^Model:/ });
    const before = await trigger.textContent();
    await trigger.click();
    await page.getByRole('dialog', { name: 'Choose a model' }).getByLabel('Search models').press('Escape');
    await expect(page.getByRole('dialog', { name: 'Choose a model' })).toBeHidden();
    await expect(trigger).toHaveText(before ?? '');
  });

  test('a tool runs with no dialog', async ({ page }) => {
    await page.goto('/');
    await openDrawerIfMobile(page);
    await openWorkspaceAndAgent(page);
    await startChat(page, 'list the files');

    await expect(page.getByLabel('tool shell')).toBeVisible();
    await expect(page.getByRole('alertdialog')).toBeHidden();
    await expect(page.getByRole('button', { name: 'Send' })).toBeVisible({ timeout: 20_000 });
  });

  test('explicit permission rule: send, confirm tool, settle', async ({ page }) => {
    await page.goto('/');
    await openDrawerIfMobile(page);
    await openWorkspaceAndAgent(page);
    await startChat(page, '/confirm list the files');

    // Streaming assistant text appears.
    await expect(page.getByLabel('assistant message')).toBeVisible();

    // The tool confirmation dialog is impossible to miss and shows the exact
    // pattern that would be granted.
    const dialog = page.getByRole('alertdialog');
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText('shell');
    await expect(dialog).toContainText('Always-allow would grant exactly:');
    await dialog.getByRole('button', { name: 'Approve once' }).click();

    await expect(dialog).toBeHidden();
    await expect(page.getByLabel('tool shell')).toBeVisible();
    // Settled: Send is back.
    await expect(page.getByRole('button', { name: 'Send' })).toBeVisible({ timeout: 20_000 });
  });

  test('stop cancels a running turn', async ({ page }) => {
    await page.goto('/');
    await openDrawerIfMobile(page);
    await openWorkspaceAndAgent(page);
    await startChat(page, '/notool take your time');

    const stop = page.getByRole('button', { name: /Stop|Stopping/ });
    await expect(stop).toBeVisible();
    await stop.click();
    await expect(page.getByRole('button', { name: 'Send' })).toBeVisible({ timeout: 20_000 });
  });

  test('resume an existing session', async ({ page }) => {
    await page.goto('/');
    await openDrawerIfMobile(page);
    await openWorkspaceAndAgent(page);
    await startChat(page, '/notool remember this');
    await expect(page.getByRole('button', { name: 'Send' })).toBeVisible({ timeout: 20_000 });

    // Reload, reopen and resume from the session list.
    await page.reload();
    await openDrawerIfMobile(page);
    await openWorkspaceAndAgent(page);
    const session = page.locator('.session-list button', { hasText: 'remember this' }).first();
    await expect(session).toBeVisible();
    await session.click();
    await expect(page).toHaveURL(/\/sessions\//);
    await expect(page.getByLabel('user message').first()).toContainText('remember this');

    // The session and workspace are part of the URL. A hard refresh restores
    // both without asking the user to reopen or reselect anything.
    const sessionURL = page.url();
    await page.reload();
    expect(page.url()).toBe(sessionURL);
    await expect(page.getByLabel('user message').first()).toContainText('remember this');

    // Browser history is routing history, not just an in-memory chat switch.
    await page.goBack();
    await expect(page).toHaveURL('/');
    await expect(page.getByLabel('Message')).toHaveCount(0);
    await page.goForward();
    await expect(page.getByLabel('user message').first()).toContainText('remember this');
  });

  test('reconnect recovers without duplicating content', async ({ page }) => {
    await page.goto('/');
    await openDrawerIfMobile(page);
    await openWorkspaceAndAgent(page);
    await startChat(page, '/notool hello there');
    // Wait for the turn to actually finish before counting, otherwise the
    // count races the stream rather than testing reconnect.
    await expect(page.getByLabel('assistant message')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Send' })).toBeVisible({ timeout: 20_000 });

    const before = await page.getByLabel(/message/).count();
    expect(before).toBeGreaterThan(0);

    // Force the EventSource to drop and reconnect.
    await page.evaluate(() => {
      window.dispatchEvent(new Event('offline'));
    });
    await page.reload();
    await openDrawerIfMobile(page);
    await openWorkspaceAndAgent(page);
    const session = page.locator('.session-list button', { hasText: 'hello there' }).first();
    await session.click();
    // Resuming rebuilds from the store: the same items, never duplicated.
    await expect(page.getByLabel('assistant message')).toBeVisible();
    const after = await page.getByLabel(/message/).count();
    expect(after).toBe(before);
  });

  test('the last workspace is remembered and reopened automatically', async ({ page }) => {
    await page.goto('/');
    await openDrawerIfMobile(page);
    await openWorkspaceAndAgent(page);

    // Reload: no typing, no clicking — the workspace comes back on its own.
    await page.reload();
    await openDrawerIfMobile(page);
    await expect(page.locator('.project-switcher')).toContainText(workspace);
    await expect(newChatButton(page)).toBeEnabled();
  });

  test('server projects are available without this browser local storage', async ({ page }) => {
    await page.goto('/');
    await openDrawerIfMobile(page);
    await openWorkspaceAndAgent(page);

    // Simulate visiting from a different device: there is no browser-local
    // workspace preference, but bootstrap still carries the server's MRU.
    await page.evaluate(() => localStorage.clear());
    await page.reload();
    await openDrawerIfMobile(page);
    await page.locator('.project-switcher').click();
    const picker = page.getByRole('dialog', { name: 'Choose a project' });
    const project = picker.getByTitle(workspace);
    await expect(project).toBeVisible();
    await project.click();
    await expect(picker).toBeHidden();
    await openDrawerIfMobile(page);
    await expect(page.locator('.project-switcher')).toContainText(workspace);

    // The selected project remains in the picker and is marked as current.
    await page.locator('.project-switcher').click();
    await expect(page.getByRole('dialog', { name: 'Choose a project' }).getByTitle(workspace)).toHaveAttribute(
      'aria-current',
      'page',
    );
  });

  test('a workspace that no longer resolves is dropped without an error', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() =>
      localStorage.setItem('dawui.prefs.v1', JSON.stringify({ recentWorkspaces: ['/nope/gone'], deliveryMode: 'normal' })),
    );
    await page.reload();
    await openDrawerIfMobile(page);
    await expect(page.locator('.banner-error')).toHaveCount(0);
    await expect(page.getByText('Select a working directory')).toBeVisible();
    expect(await page.evaluate(() => JSON.parse(localStorage.getItem('dawui.prefs.v1') ?? '{}').recentWorkspaces)).toEqual([]);
  });

  // Guards a specificity trap: the generic button:hover rule is more specific
  // than a bare variant class, so a filled button can silently lose its fill
  // on hover and end up with unreadable text.
  test('filled buttons stay readable on hover', async ({ page, viewport }) => {
    test.skip((viewport?.width ?? 1280) <= 820, 'hover is a pointer affordance');
    await page.goto('/');
    await openWorkspaceAndAgent(page);
    await startChat(page, '/notool hello');
    // Wait for the turn to settle: Send is briefly still on screen right after
    // the click, before the run status turns it into Stop.
    await expect(page.getByLabel('assistant message')).toBeVisible();
    await expect(page.getByRole('button', { name: /Stop/ })).toHaveCount(0, { timeout: 20_000 });
    await expect(page.locator('.send-btn')).toBeVisible();

    const contrast = (sel: string) => {
      const el = document.querySelector(sel);
      if (!el) return 0;
      const cs = getComputedStyle(el);
      const parse = (c: string) => (c.match(/[\d.]+/g) ?? []).slice(0, 3).map(Number);
      const lum = (rgb: number[]) => {
        const [r, g, b] = rgb.map((v) => {
          const n = v / 255;
          return n <= 0.03928 ? n / 12.92 : Math.pow((n + 0.055) / 1.055, 2.4);
        });
        return 0.2126 * (r ?? 0) + 0.7152 * (g ?? 0) + 0.0722 * (b ?? 0);
      };
      let bg = cs.backgroundColor;
      if (bg.startsWith('rgba') && bg.endsWith(', 0)') && el.parentElement) {
        bg = getComputedStyle(el.parentElement).backgroundColor;
      }
      const a = lum(parse(cs.color));
      const b = lum(parse(bg));
      return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
    };

    await page.locator('.send-btn').hover();
    expect(await page.evaluate(contrast, '.send-btn')).toBeGreaterThan(4.5);

    // "Jump to latest" only exists once the conversation overflows, so make it
    // overflow, then scroll away from the bottom.
    await page.setViewportSize({ width: 1280, height: 400 });
    for (const text of ['/notool second', '/notool third', '/notool fourth', '/notool fifth']) {
      const assistantMessages = page.getByLabel('assistant message');
      const previousCount = await assistantMessages.count();
      await composer(page).fill(text);
      await page.getByRole('button', { name: 'Send' }).click();
      // Observing the new turn before checking for idle prevents the idle check
      // from passing during the brief interval before running state arrives.
      await expect(assistantMessages).toHaveCount(previousCount + 1, { timeout: 20_000 });
      await expect(page.getByRole('button', { name: /Stop/ })).toHaveCount(0, { timeout: 20_000 });
    }
    await page.locator('.conversation').evaluate((el) => {
      el.scrollTop = 0;
      el.dispatchEvent(new Event('scroll'));
    });
    const jump = page.locator('.jump');
    await expect(jump).toBeVisible();
    await jump.hover();
    expect(await page.evaluate(contrast, '.jump')).toBeGreaterThan(4.5);
  });

  test('keyboard focus reaches the composer', async ({ page }) => {
    await page.goto('/');
    await openDrawerIfMobile(page);
    await openWorkspaceAndAgent(page);
    await startChat(page, 'typed with the keyboard');
    await composer(page).focus();
    await expect(composer(page)).toBeFocused();
  });
});

test.describe('mobile', () => {
  test.skip(({ viewport }) => (viewport?.width ?? 1280) > 820, 'mobile-only');

  test('drawer opens, closes with Escape, and the composer works', async ({ page }) => {
    await page.goto('/');
    const menu = page.getByRole('button', { name: 'Menu' });
    await expect(menu).toBeVisible();
    await menu.click();
    await expect(menu).toHaveAttribute('aria-expanded', 'true');

    await openWorkspaceAndAgent(page);
    await startChat(page, '/notool from my phone');
    // The drawer closes when a chat opens.
    await expect(menu).toHaveAttribute('aria-expanded', 'false');

    await menu.click();
    await page.keyboard.press('Escape');
    await expect(menu).toHaveAttribute('aria-expanded', 'false');

    await expect(page.getByLabel('assistant message')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Send' })).toBeVisible({ timeout: 20_000 });
  });

  test('layout holds at 320px', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 700 });
    await page.goto('/');
    const scrollWidth = await page.evaluate(() => document.documentElement.scrollWidth);
    expect(scrollWidth).toBeLessThanOrEqual(321);
  });
});
