import { spawn, type ChildProcess } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { mkdtemp, readFile, readdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { basename, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

const web = fileURLToPath(new URL('..', import.meta.url));
const api = resolve(web, '../api');
const repository = resolve(web, '../..');

function run(command: string, args: string[], options: { cwd?: string; env?: NodeJS.ProcessEnv; input?: string } = {}): Promise<string> {
 return new Promise((done, fail) => {
  const process = spawn(command, args, { cwd: options.cwd ?? repository, env: { ...globalThis.process.env, ...options.env }, windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'] });
  let output = '';
  process.stdout.on('data', chunk => { output += String(chunk); });
  // Do not include process arguments, SQL input, or environment in errors.
  process.stderr.resume();
  process.stdin.on('error', () => undefined);
  process.on('error', () => fail(new Error(`Unable to start ${basename(command)}.`)));
  process.on('exit', code => code === 0 ? done(output.trim()) : fail(new Error(`${basename(command)} failed (${code}).`)));
  process.stdin.end(options.input ?? '');
 });
}
async function ready(url: string, child?: ChildProcess) {
 for (let attempt = 0; attempt < 100; attempt++) {
  if (child && child.exitCode !== null) throw new Error('Test service exited before readiness.');
  try { if ((await fetch(url)).ok) return; } catch { /* bounded startup retry */ }
  await new Promise(resolve => setTimeout(resolve, 300));
 }
 throw new Error('Test service did not become ready.');
}

export default async function setup() {
 const workspace = await mkdtemp(resolve(tmpdir(), 'full-stack-file-vault-e2e-'));
 const container = 'full-stack-file-vault-browser-' + randomBytes(6).toString('hex');
 const password = randomBytes(24).toString('hex');
 const runtimePassword = randomBytes(24).toString('hex');
 const accountPassword = randomBytes(24).toString('hex');
 const children: ChildProcess[] = [];
 let containerCreated = false;
 async function cleanup() {
  for (const child of children.reverse()) {
   if (child.exitCode === null) await new Promise<void>(resolve => {
    const timer = setTimeout(resolve, 3000);
    child.once('exit', () => { clearTimeout(timer); resolve(); });
    child.kill();
   });
  }
  if (containerCreated) await run('docker', ['rm', '-f', '-v', container]);
  // Windows cleanup stays inside the exact randomly allocated temporary directory.
  const absolute = resolve(workspace);
  const allowed = resolve(tmpdir()) + sep + 'full-stack-file-vault-e2e-';
  if (!absolute.toLowerCase().startsWith(allowed.toLowerCase())) throw new Error('Unsafe test cleanup path.');
  await rm(absolute, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
 }
 try {
  console.log('[e2e] Starting isolated PostgreSQL');
  await run('docker', ['run', '-d', '--name', container, '-e', 'POSTGRES_PASSWORD', '-e', 'POSTGRES_DB=vault_browser', '-p', '127.0.0.1::5432', 'postgres:17-bookworm'], { env: { POSTGRES_PASSWORD: password } });
  containerCreated = true;
  let databaseReady = false;
  for (let i = 0; i < 60; i++) {
   try { await run('docker', ['exec', container, 'pg_isready', '-h', '127.0.0.1', '-U', 'postgres', '-d', 'vault_browser']); databaseReady = true; break; }
   catch { await new Promise(resolve => setTimeout(resolve, 500)); }
  }
  if (!databaseReady) throw new Error('Test PostgreSQL failed to start.');
  const sql = (input: string) => run('docker', ['exec', '-i', container, 'psql', '-v', 'ON_ERROR_STOP=1', '-U', 'postgres', '-d', 'vault_browser'], { input });
  const migrationDirectory = resolve(repository, 'db/migrations');
  for (const name of (await readdir(migrationDirectory)).filter(name => name.endsWith('.up.sql')).sort()) {
   await sql(await readFile(resolve(migrationDirectory, name), 'utf8'));
  }
  await sql(`ALTER ROLE vault_runtime LOGIN PASSWORD '${runtimePassword}';`);
  const host = await run('docker', ['port', container, '5432/tcp']);
  const operatorURL = `postgres://postgres:${password}@${host}/vault_browser?sslmode=disable`;
  const runtimeURL = `postgres://vault_runtime:${runtimePassword}@${host}/vault_browser?sslmode=disable`;
  const extension = process.platform === 'win32' ? '.exe' : '';
  const server = resolve(workspace, 'server' + extension);
  const provision = resolve(workspace, 'provision' + extension);
  console.log('[e2e] Building API and provisioning test accounts');
  await run('go', ['build', '-o', server, './cmd/server'], { cwd: api });
  await run('go', ['build', '-o', provision, './cmd/provision-user'], { cwd: api });
  for (const [name, role] of [['e2e.owner', 'USER'], ['e2e.admin', 'ADMIN'], ['e2e.recipient', 'USER'], ['e2e.retry', 'USER']]) {
   await run(provision, ['-login', name, '-role', role], { env: { PROVISION_DATABASE_URL: operatorURL }, input: accountPassword });
  }
  process.env.E2E_PASSWORD = accountPassword;
  const backend = spawn(server, [], {
   cwd: workspace, windowsHide: true, stdio: 'ignore',
   env: { ...process.env, DATABASE_URL: runtimeURL, APP_ENV: 'development', HTTP_ADDR: '127.0.0.1:18881', PUBLIC_ORIGIN: 'http://127.0.0.1:4173', BLOB_STORAGE_DIR: resolve(workspace, 'blobs'), UPLOAD_STAGING_DIR: resolve(workspace, 'staging') },
  });
  children.push(backend);
  await ready('http://127.0.0.1:18881/readyz', backend);
  const frontend = spawn(process.execPath, [resolve(web, 'node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', '4173', '--strictPort'], {
   cwd: web, windowsHide: true, stdio: 'ignore',
   env: { ...process.env, API_PROXY_TARGET: 'http://127.0.0.1:18881', WEB_PORT: '4173' },
  });
  children.push(frontend);
  await ready('http://127.0.0.1:4173/', frontend);
  console.log('[e2e] Real API and frontend ready');
  return cleanup;
 } catch (error) { await cleanup(); throw error; }
}
