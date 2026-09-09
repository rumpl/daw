const { app, BrowserWindow, dialog, Menu, nativeImage, protocol, shell, Tray } = require('electron');
const { spawn, spawnSync } = require('node:child_process');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const { Readable } = require('node:stream');

const APP_SCHEME = 'daw';
const APP_HOST = 'localhost';
const WINDOW_ROUTE_FILE = 'last-window-route';
// Pulling the published sandbox kit on its first use may take longer than a
// cached sandbox start.
const STARTUP_TIMEOUT_MS = 180_000;
const APP_ICON = app.isPackaged
  ? path.join(process.resourcesPath, 'icon.png')
  : path.join(__dirname, 'build', 'icon.png');
const TRAY_ICON = app.isPackaged
  ? path.join(process.resourcesPath, 'trayTemplate.png')
  : path.join(__dirname, 'build', 'trayTemplate.png');

// Register before app readiness so Chromium treats daw:// like a normal secure
// origin. This is required for module scripts, dynamic plugin imports, fetch,
// and streaming SSE responses.
protocol.registerSchemesAsPrivileged([
  {
    scheme: APP_SCHEME,
    privileges: {
      standard: true,
      secure: true,
      supportFetchAPI: true,
      corsEnabled: true,
      stream: true,
      codeCache: true,
    },
  },
]);

let backend = null;
let socketDirectory = null;
let tray = null;
let quitting = false;

function dashboardPath() {
  if (app.isPackaged) return path.join(process.resourcesPath, 'backend', 'dawui');
  return path.resolve(__dirname, '..', 'bin', 'dawui');
}

function sandboxLauncherPath() {
  if (app.isPackaged) return path.join(process.resourcesPath, 'backend', 'daw-sandbox');
  return path.resolve(__dirname, '..', 'bin', 'daw-sandbox');
}

function runnerKitReference() {
  const referenceFile = app.isPackaged
    ? path.join(process.resourcesPath, 'backend', 'daw-runner-kit.ref')
    : path.resolve(__dirname, '..', 'bin', 'daw-runner-kit.ref');
  if (!fs.existsSync(referenceFile)) {
    throw new Error(`Sandbox kit reference not found at ${referenceFile}. Run \`make electron\` from the repository root.`);
  }
  const reference = fs.readFileSync(referenceFile, 'utf8').trim();
  if (!reference) throw new Error(`Sandbox kit reference is empty at ${referenceFile}.`);
  return reference;
}

function makeSocketPath() {
  // A short temporary path stays below macOS's Unix-socket path limit. The
  // owner-only directory and the backend's 0600 socket mode prevent other
  // local users from reaching the API.
  socketDirectory = fs.mkdtempSync(path.join(os.tmpdir(), 'dawui-'));
  fs.chmodSync(socketDirectory, 0o700);
  return path.join(socketDirectory, 'daw.sock');
}

function loginShellEnvironment() {
  if (process.platform === 'win32') return {};

  const shell = process.env.SHELL || (process.platform === 'darwin' ? '/bin/zsh' : '/bin/sh');
  if (!path.isAbsolute(shell) || !fs.existsSync(shell)) return {};

  // Apps opened from Finder/Dock inherit a minimal launchd environment rather
  // than the user's terminal environment. docker-agent model credentials and
  // tools such as node/docker are commonly configured by shell startup files,
  // so import the login shell environment before starting the backend. A NUL
  // marker makes this robust to shell themes printing text during startup.
  const marker = '__DAW_LOGIN_ENV_START__';
  const result = spawnSync(shell, ['-l', '-i', '-c', `printf '${marker}\\0'; /usr/bin/env -0`], {
    env: process.env,
    encoding: 'buffer',
    timeout: 10_000,
    maxBuffer: 4 * 1024 * 1024,
  });
  if (result.error || result.status !== 0) {
    console.warn('[electron] could not read login shell environment:', result.error?.message || `exit ${result.status}`);
    return {};
  }

  const markerBytes = Buffer.from(`${marker}\0`);
  const markerAt = result.stdout.indexOf(markerBytes);
  if (markerAt < 0) {
    console.warn('[electron] login shell did not return an environment');
    return {};
  }

  const environment = {};
  const payload = result.stdout.subarray(markerAt + markerBytes.length).toString('utf8');
  for (const entry of payload.split('\0')) {
    const separator = entry.indexOf('=');
    if (separator <= 0) continue;
    const name = entry.slice(0, separator);
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) continue;
    if (name === 'PWD' || name === 'OLDPWD' || name === 'SHLVL' || name === '_') continue;
    environment[name] = entry.slice(separator + 1);
  }
  return environment;
}

function startBackend(socketPath) {
  const executable = sandboxLauncherPath();
  const dashboard = dashboardPath();
  const kit = runnerKitReference();
  for (const required of [executable, dashboard]) {
    if (!fs.existsSync(required)) {
      throw new Error(`Sandbox backend resource not found at ${required}. Run \`make electron\` from the repository root.`);
    }
  }

  // The desktop app exposes both execution targets, but provisions one Docker
  // Sandbox per session and selects it as the default. Mounting the user's home
  // as the sandbox workspace lets projects selected later in the UI remain
  // available without restarting the Electron host.
  backend = spawn(executable, [
    '-per-session',
    '-workspace', os.homedir(),
    '-dashboard', dashboard,
    '-kit', kit,
  ], {
    env: {
      ...process.env,
      ...loginShellEnvironment(),
      DAWUI_SOCKET: socketPath,
      DAWUI_ELECTRON: '1',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
    windowsHide: true,
  });
  backend.stdout.on('data', (data) => process.stdout.write(`[backend] ${data}`));
  backend.stderr.on('data', (data) => process.stderr.write(`[backend] ${data}`));
  backend.once('error', (error) => {
    if (!quitting) dialog.showErrorBox('Unable to start Atelier', error.message);
  });
  backend.once('exit', (code, signal) => {
    backend = null;
    if (quitting) return;
    dialog.showErrorBox(
      'Atelier backend stopped',
      `The backend exited unexpectedly (${signal || `code ${code}`}).`,
    );
    app.quit();
  });
}

function healthCheck(socketPath) {
  return new Promise((resolve, reject) => {
    const request = http.request({
      socketPath,
      path: '/api/health',
      method: 'GET',
      headers: { Host: APP_HOST },
    }, (response) => {
      response.resume();
      response.once('end', () => {
        if (response.statusCode === 200) resolve();
        else reject(new Error(`health check returned ${response.statusCode}`));
      });
    });
    request.once('error', reject);
    request.end();
  });
}

async function waitForBackend(socketPath) {
  const deadline = Date.now() + STARTUP_TIMEOUT_MS;
  let lastError = new Error('backend did not create its socket');
  while (Date.now() < deadline) {
    if (!backend) throw new Error('backend exited during startup');
    try {
      await healthCheck(socketPath);
      return;
    } catch (error) {
      lastError = error;
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
  }
  throw new Error(`Backend startup timed out: ${lastError.message}`);
}

const requestHeadersToSkip = new Set([
  'connection',
  'content-length',
  'host',
  'origin',
  'referer',
  'sec-fetch-dest',
  'sec-fetch-mode',
  'sec-fetch-site',
  'transfer-encoding',
]);
const responseHeadersToSkip = new Set([
  'connection',
  'keep-alive',
  'transfer-encoding',
  'upgrade',
]);

async function proxyToBackend(request, socketPath) {
  const url = new URL(request.url);
  if (url.protocol !== `${APP_SCHEME}:` || url.hostname !== APP_HOST) {
    return new Response('Not found', { status: 404 });
  }

  const headers = {};
  for (const [name, value] of request.headers) {
    if (!requestHeadersToSkip.has(name.toLowerCase())) headers[name] = value;
  }
  headers.Host = APP_HOST;

  const hasBody = request.method !== 'GET' && request.method !== 'HEAD';
  const payload = hasBody ? Buffer.from(await request.arrayBuffer()) : undefined;
  if (payload) headers['Content-Length'] = String(payload.byteLength);

  return new Promise((resolve) => {
    const upstream = http.request({
      socketPath,
      path: url.pathname + url.search,
      method: request.method,
      headers,
      signal: request.signal,
    }, (response) => {
      const responseHeaders = new Headers();
      for (const [name, value] of Object.entries(response.headers)) {
        if (responseHeadersToSkip.has(name.toLowerCase()) || value === undefined) continue;
        if (Array.isArray(value)) {
          for (const item of value) responseHeaders.append(name, item);
        } else {
          responseHeaders.set(name, value);
        }
      }
      const noBody = request.method === 'HEAD' || response.statusCode === 204 || response.statusCode === 304;
      resolve(new Response(noBody ? null : Readable.toWeb(response), {
        status: response.statusCode || 500,
        statusText: response.statusMessage,
        headers: responseHeaders,
      }));
    });
    upstream.once('error', (error) => {
      const status = error.name === 'AbortError' ? 499 : 502;
      resolve(new Response(status === 499 ? '' : 'Backend unavailable', { status }));
    });
    upstream.end(payload);
  });
}

function lastWindowURL() {
  const fallback = new URL(`${APP_SCHEME}://${APP_HOST}/`);
  try {
    const saved = new URL(fs.readFileSync(path.join(app.getPath('userData'), WINDOW_ROUTE_FILE), 'utf8').trim());
    if (saved.protocol === `${APP_SCHEME}:` && saved.hostname === APP_HOST) {
      fallback.pathname = saved.pathname;
      fallback.search = saved.search;
      fallback.hash = saved.hash;
    }
  } catch {
    // A first launch or an invalid state file should simply open the dashboard.
  }
  fallback.searchParams.set('electron', '1');
  return fallback.toString();
}

function rememberWindowURL(window) {
  try {
    const current = new URL(window.webContents.getURL());
    if (current.protocol !== `${APP_SCHEME}:` || current.hostname !== APP_HOST) return;
    current.searchParams.delete('electron');
    fs.mkdirSync(app.getPath('userData'), { recursive: true });
    fs.writeFileSync(path.join(app.getPath('userData'), WINDOW_ROUTE_FILE), current.toString());
  } catch {
    // Restoring the route is a convenience and must not prevent the window closing.
  }
}

function createWindow() {
  const window = new BrowserWindow({
    width: 1440,
    height: 960,
    minWidth: 800,
    minHeight: 600,
    title: 'Atelier',
    icon: APP_ICON,
    backgroundColor: '#111111',
    show: false,
    autoHideMenuBar: true,
    titleBarStyle: process.platform === 'darwin' ? 'hiddenInset' : 'default',
    ...(process.platform === 'darwin' ? { trafficLightPosition: { x: 14, y: 7 } } : {}),
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });

  window.setMenuBarVisibility(false);
  window.on('close', () => rememberWindowURL(window));
  window.once('ready-to-show', () => window.show());
  window.webContents.setWindowOpenHandler(({ url }) => {
    if (url.startsWith('https://') || url.startsWith('http://') || url.startsWith('mailto:')) {
      void shell.openExternal(url);
    }
    return { action: 'deny' };
  });
  window.webContents.on('will-navigate', (event, url) => {
    const target = new URL(url);
    if (target.protocol !== `${APP_SCHEME}:` || target.hostname !== APP_HOST) {
      event.preventDefault();
      if (target.protocol === 'https:' || target.protocol === 'http:') void shell.openExternal(url);
    }
  });
  void window.loadURL(lastWindowURL());
  return window;
}

function showWindow() {
  let window = BrowserWindow.getAllWindows()[0];
  if (!window) window = createWindow();
  if (window.isMinimized()) window.restore();
  window.show();
  window.focus();
}

function createTray() {
  if (process.platform !== 'darwin') return;

  const trayIcon = nativeImage.createFromPath(TRAY_ICON);
  trayIcon.setTemplateImage(true);
  tray = new Tray(trayIcon);
  tray.setToolTip('Atelier');

  const contextMenu = Menu.buildFromTemplate([
    {
      label: 'Quit Atelier',
      click: () => app.quit(),
    },
  ]);
  tray.on('click', showWindow);
  tray.on('right-click', () => tray.popUpContextMenu(contextMenu));
}

function createApplicationMenu() {
  const template = [];
  if (process.platform === 'darwin') template.push({ role: 'appMenu' });
  template.push({ role: 'fileMenu' });
  template.push({ role: 'editMenu' });
  template.push({
    label: 'View',
    submenu: [
      { role: 'resetZoom' },
      { role: 'zoomIn' },
      { role: 'zoomOut' },
      { type: 'separator' },
      { role: 'togglefullscreen' },
    ],
  });
  return Menu.buildFromTemplate(template);
}

async function main() {
  // Native edit and zoom shortcuts are application-menu roles. Keep the menu
  // installed even when its menu bar is hidden on Windows and Linux.
  Menu.setApplicationMenu(createApplicationMenu());
  const socketPath = makeSocketPath();
  startBackend(socketPath);
  await waitForBackend(socketPath);
  await protocol.handle(APP_SCHEME, (request) => proxyToBackend(request, socketPath));
  if (process.platform === 'darwin') app.dock.setIcon(APP_ICON);
  createTray();
  createWindow();

  app.on('activate', showWindow);
}

const gotLock = app.requestSingleInstanceLock();
if (!gotLock) {
  app.quit();
} else {
  app.on('second-instance', showWindow);

  app.whenReady().then(main).catch((error) => {
    dialog.showErrorBox('Unable to start Atelier', error.stack || error.message);
    app.quit();
  });
}

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});

app.on('before-quit', () => {
  quitting = true;
  if (backend) backend.kill('SIGTERM');
});

app.on('will-quit', () => {
  if (backend) backend.kill('SIGKILL');
  if (socketDirectory) fs.rmSync(socketDirectory, { recursive: true, force: true });
});
