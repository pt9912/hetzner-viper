import { execFileSync, spawn } from 'node:child_process';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { freePort, UI } from './fixture';

// A viper ui on real Docker (PROVIDER=compose): own names (prefix m5t), an own home directory (so own ssh keys and own state),
// nothing of the user's is touched. For the tests that are run by hand (E2E_REAL_COMPOSE=1); CI has no Docker image of the
// machines and does not run them.
export async function startRealCompose(image: string): Promise<UI & { prefix: string }> {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'viper-e2e-real-'));
  const home = path.join(root, 'home');
  const work = path.join(root, 'work');
  for (const d of [home, path.join(work, 'machines')]) { fs.mkdirSync(d, { recursive: true }); }
  const prefix = 'm5t';
  fs.writeFileSync(path.join(work, '.env.local'), `PROVIDER=compose\nSERVER_PREFIX=${prefix}\nSERVER_LOCATION=default\nCOMPOSE_IMAGE=${image}\nDNS_PROVIDER=none\nCOMPOSE_DOCKER=none\nVIPER_HOMEBREW=0\n`, { mode: 0o600 });
  fs.writeFileSync(path.join(work, 'machines', 'web.env.local'), '', { mode: 0o600 });
  const viper = process.env.VIPER_BIN as string;
  const env = { ...process.env, HOME: home };
  execFileSync(viper, ['setup', '--dir', work], { cwd: work, env, stdio: 'pipe' }); // the login key and the host key of this home
  const child = spawn(viper, ['ui', '--no-open', '--port', String(await freePort())], { cwd: work, env });
  let out = '';
  child.stdout?.on('data', (d) => { out += d; });
  child.stderr?.on('data', (d) => { out += d; });
  const deadline = Date.now() + 15000;
  let m: RegExpMatchArray | null = null;
  while (!(m = out.match(/(http:\/\/127\.0\.0\.1:\d+)\/\?t=[0-9a-f]+/)) && Date.now() < deadline) { await new Promise((r) => setTimeout(r, 50)); }
  if (!m) { child.kill('SIGKILL'); throw new Error('viper ui did not start: ' + out); }
  return {
    base: m[1], start: m[0], home, work, prefix, output: () => out,
    stop: async () => {
      if (child.exitCode === null) {
        child.kill('SIGINT');
        await new Promise((r) => { child.on('exit', r); setTimeout(() => { child.kill('SIGKILL'); r(null); }, 60000); });
      }
      // what the test may have left: delete by name, with this prefix only
      try {
        execFileSync(viper, ['down', 'web', '--force', '--dir', work], { cwd: work, env, input: 'DELETE\nDELETE\n', stdio: 'pipe', timeout: 180000 });
      } catch { /* nothing was left, or it asks something else: the test checks docker itself */ }
      fs.rmSync(root, { recursive: true, force: true });
    },
  };
}
