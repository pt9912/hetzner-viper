import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as path from 'node:path';

// builds the program once: VIPER_BIN names one that exists already
export default async function setup() {
  if (process.env.VIPER_BIN) { return; }
  const root = path.resolve(__dirname, '..');
  const out = path.join(__dirname, '.build');
  fs.mkdirSync(out, { recursive: true });
  const bin = path.join(out, process.platform === 'win32' ? 'viper.exe' : 'viper');
  execFileSync('go', ['build', '-o', bin, './cmd/viper'], { cwd: root, stdio: 'inherit' });
  process.env.VIPER_BIN = bin;
}
