import { chromium } from '@playwright/test';
import { mkdirSync, writeFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

const root = join(homedir(), '.dawui-shots');
mkdirSync(root, { recursive: true });
writeFileSync(join(root, 'agent.yaml'), 'agents:\n  root: {}\n');

const base = 'http://127.0.0.1:4797';
const b = await chromium.launch();

async function run(name, viewport, mobile) {
  const ctx = await b.newContext({ viewport, colorScheme: 'dark', deviceScaleFactor: 2 });
  const p = await ctx.newPage();
  await p.goto(base);
  if (mobile) await p.getByRole('button', { name: 'Menu' }).click();
  await p.getByLabel('Working directory path').fill(root);
  await p.getByRole('button', { name: 'Open' }).click();
  await p.getByRole('button', { name: 'New chat', exact: true }).click();
  await p.getByRole('textbox', { name: 'Message' }).fill('Run some random tools, I am testing my web ui');
  await p.getByRole('button', { name: 'Send' }).click();
  await p.waitForTimeout(1800);
  await p.getByRole('textbox', { name: 'Message' }).fill('And now explain the result in **markdown** with a `code` span');
  await p.getByRole('button', { name: 'Send' }).click();
  await p.waitForTimeout(2200);
  await p.screenshot({ path: `/tmp/uishots/${name}.png`, fullPage: false });
  await ctx.close();
}

await run('desktop', { width: 1440, height: 900 }, false);
await run('mobile', { width: 390, height: 844 }, true);
await b.close();
