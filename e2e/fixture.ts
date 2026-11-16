import { ChildProcess, spawn } from 'node:child_process';
import * as fs from 'node:fs';
import * as net from 'node:net';
import * as os from 'node:os';
import * as path from 'node:path';
import { pathToFileURL } from 'node:url';

const win = process.platform === 'win32';
// the program that opens the browser for viper ui, which the tests fake (Windows: rundll32 cannot be faked, those tests are skipped)
export const opener = process.platform === 'darwin' ? 'open' : 'xdg-open';
export const canFakeOpener = !win;

export interface UI {
  base: string;      // http://127.0.0.1:<port>
  start: string;     // the address with the one-time key
  home: string;
  work: string;
  root: string;
  stop(): Promise<void>;
  output(): string;
}

// startUI makes a directory of settings for PROVIDER=compose with a fake docker (it only prints and sleeps), and starts
// viper ui on it. Nothing is made on any machine; every run of viper there ends at the fake.
export async function startUI(extra: string[] = [], dockerSleep = 30, openBrowser = false, opts: { objects?: boolean; terminal?: boolean } = {}): Promise<UI> {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'viper-e2e-'));
  const home = path.join(root, 'home');
  const work = path.join(root, 'work');
  const bin = path.join(root, 'bin');
  for (const d of [home, path.join(work, 'machines'), path.join(work, 'groups'), bin]) { fs.mkdirSync(d, { recursive: true }); }
  fs.writeFileSync(path.join(work, '.env.local'), 'PROVIDER=compose\nSERVER_PREFIX=e2e\nSERVER_LOCATION=default\nCOMPOSE_IMAGE=e2efake:1\nDNS_PROVIDER=none\nVIPER_GH_TOKEN=ghp_e2esecretvalue123\n', { mode: 0o600 });
  fs.writeFileSync(path.join(work, 'machines', 'web.env.local'), '');
  for (const m of ['a', 'b']) { fs.writeFileSync(path.join(work, 'machines', m + '.env.local'), 'NATS_ENABLED=true\nNATS_GROUP=dev\nNATS_MCP=true\n'); }
  fs.writeFileSync(path.join(work, 'groups', 'dev.env'), `GROUP_MACHINES=a,b\n${opts.objects === false ? '' : 'NATS_OBJECTS=true\n'}`);
  // the fake docker only says that it was called and then waits (a run that is cancelled has something to cancel)
  if (win) {
    fs.writeFileSync(path.join(bin, 'docker.cmd'), `@echo off\r\necho fake docker %* token=ghp_e2esecretvalue123 1>&2\r\n${dockerSleep > 0 ? `ping -n ${dockerSleep + 1} 127.0.0.1 >nul\r\n` : ''}exit /b 0\r\n`);
  } else {
    fs.writeFileSync(path.join(bin, 'docker'), `#!/bin/sh\necho "fake docker $* token=ghp_e2esecretvalue123" >&2\nexec sleep ${dockerSleep}\n`, { mode: 0o755 });
    // the browser is "opened" by a fake that keeps a copy of the start file it is given
    fs.writeFileSync(path.join(bin, opener), `#!/bin/sh\ncp "$1" ${path.join(root, 'launch.html')}\n`, { mode: 0o755 });
  }
  // a fake terminal program (Linux): it keeps its arguments, one per line, and ends; the machine web has an SSH configuration
  if (opts.terminal && process.platform === 'linux') {
    fs.writeFileSync(path.join(bin, 'gnome-terminal'), `#!/bin/sh\nprintf '%s\\n' "$@" > ${path.join(root, 'terminal.log')}\n`, { mode: 0o755 });
    fs.mkdirSync(path.join(home, '.ssh'), { recursive: true });
    fs.writeFileSync(path.join(home, '.ssh', 'config_hetzner_e2e-web'), '');
  }
  const stateDir = path.join(home, '.local', 'state', 'viper');
  fs.mkdirSync(stateDir, { recursive: true });
  fs.writeFileSync(path.join(stateDir, 'events.log'), '2026-11-14T10:00:00+00:00 e2e-web   up   start \u001b[31mred ghp_e2esecretvalue123\n');

  const viper = process.env.VIPER_BIN as string;
  const child: ChildProcess = spawn(viper, ['ui', ...(openBrowser ? [] : ['--no-open']), '--port', String(await freePort()), ...extra], {
    cwd: work,
    env: envWith({ HOME: home, USERPROFILE: home, ...(opts.terminal ? { DISPLAY: ':99' } : {}) }, bin),
  });
  let out = '';
  child.stdout?.on('data', (d) => { out += d; });
  child.stderr?.on('data', (d) => { out += d; });
  const deadline = Date.now() + 15000;
  const launch = path.join(root, 'launch.html');
  const pattern = openBrowser ? /(http:\/\/127\.0\.0\.1:\d+)\// : /(http:\/\/127\.0\.0\.1:\d+)\/\?t=[0-9a-f]+/;
  let m: RegExpMatchArray | null = null;
  while (!((m = out.match(pattern)) && (!openBrowser || fs.existsSync(launch))) && Date.now() < deadline) {
    await new Promise((r) => setTimeout(r, 50));
  }
  if (!m) { child.kill('SIGKILL'); throw new Error('viper ui did not start: ' + out); }
  return {
    base: m[1], start: openBrowser ? pathToFileURL(launch).href : m[0], home, work, root,
    output: () => out,
    stop: async () => {
      if (child.exitCode === null) {
        child.kill(win ? undefined : 'SIGINT'); // Windows has no signal to give: the process is ended
        await new Promise((r) => { child.on('exit', r); setTimeout(() => { child.kill('SIGKILL'); r(null); }, 30000); });
      }
      try {
        fs.rmSync(root, { recursive: true, force: true, maxRetries: 10, retryDelay: 300 });
      } catch { /* on Windows a fake docker may still hold its folder: the system's temp directory cleans up later */ }
    },
  };
}

// freePort asks the system for a free port (and gives it back at once, for viper ui to take it).
export function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.once('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const port = (srv.address() as net.AddressInfo).port;
      srv.close(() => resolve(port));
    });
  });
}

// envWith is the environment of this process with some variables set, and the folder first in the search path (the name of the
// variable is Path on Windows, and a second one would not do).
export function envWith(vars: Record<string, string>, firstInPath: string): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = { ...process.env, ...vars };
  const key = Object.keys(env).find((k) => k.toLowerCase() === 'path') ?? 'PATH';
  env[key] = firstInPath + path.delimiter + (env[key] ?? '');
  return env;
}
