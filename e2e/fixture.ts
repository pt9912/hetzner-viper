import { ChildProcess, spawn } from 'node:child_process';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

export interface UI {
  base: string;      // http://127.0.0.1:<port>
  start: string;     // the address with the one-time key
  home: string;
  work: string;
  stop(): Promise<void>;
  output(): string;
}

// startUI makes a directory of settings for PROVIDER=compose with a fake docker (it only prints and sleeps), and starts
// viper ui on it. Nothing is made on any machine; every run of viper there ends at the fake.
export async function startUI(extra: string[] = [], dockerSleep = 30, openBrowser = false): Promise<UI> {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'viper-e2e-'));
  const home = path.join(root, 'home');
  const work = path.join(root, 'work');
  const bin = path.join(root, 'bin');
  for (const d of [home, path.join(work, 'machines'), path.join(work, 'groups'), bin]) { fs.mkdirSync(d, { recursive: true }); }
  fs.writeFileSync(path.join(work, '.env.local'), 'PROVIDER=compose\nSERVER_PREFIX=e2e\nSERVER_LOCATION=default\nCOMPOSE_IMAGE=e2efake:1\nDNS_PROVIDER=none\nVIPER_GH_TOKEN=ghp_e2esecretvalue123\n', { mode: 0o600 });
  fs.writeFileSync(path.join(work, 'machines', 'web.env.local'), '');
  for (const m of ['a', 'b']) { fs.writeFileSync(path.join(work, 'machines', m + '.env.local'), 'NATS_ENABLED=true\nNATS_GROUP=dev\nNATS_MCP=true\n'); }
  fs.writeFileSync(path.join(work, 'groups', 'dev.env'), 'GROUP_MACHINES=a,b\n');
  fs.writeFileSync(path.join(bin, 'docker'), `#!/bin/sh\necho "fake docker $* token=ghp_e2esecretvalue123" >&2\nsleep ${dockerSleep}\n`, { mode: 0o755 });
  // the browser is "opened" by a fake xdg-open that keeps a copy of the start file it is given
  fs.writeFileSync(path.join(bin, 'xdg-open'), `#!/bin/sh\ncp "$1" ${path.join(root, 'launch.html')}\n`, { mode: 0o755 });
  const stateDir = path.join(home, '.local', 'state', 'viper');
  fs.mkdirSync(stateDir, { recursive: true });
  fs.writeFileSync(path.join(stateDir, 'events.log'), '2026-11-14T10:00:00+00:00 e2e-web   up   start \u001b[31mred ghp_e2esecretvalue123\n');

  const viper = process.env.VIPER_BIN as string;
  const child: ChildProcess = spawn(viper, ['ui', ...(openBrowser ? [] : ['--no-open']), '--port', '0'.replace('0', String(20000 + Math.floor(Math.random() * 20000))), ...extra], {
    cwd: work,
    env: { ...process.env, HOME: home, PATH: bin + path.delimiter + process.env.PATH },
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
    base: m[1], start: openBrowser ? 'file://' + launch : m[0], home, work,
    output: () => out,
    stop: async () => {
      if (child.exitCode === null) {
        child.kill('SIGINT');
        await new Promise((r) => { child.on('exit', r); setTimeout(() => { child.kill('SIGKILL'); r(null); }, 30000); });
      }
      fs.rmSync(root, { recursive: true, force: true });
    },
  };
}
