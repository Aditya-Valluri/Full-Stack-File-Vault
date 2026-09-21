// Isolated Compose rehearsal only. Production upload requires separately configured object storage.
import { spawn } from 'node:child_process';
import { createReadStream, createWriteStream } from 'node:fs';
import { mkdir, readFile, writeFile, appendFile, rm } from 'node:fs/promises';
import { randomBytes, createCipheriv } from 'node:crypto';
import { pipeline } from 'node:stream/promises';
import { resolve, sep } from 'node:path';
import { pathToFileURL } from 'node:url';

const root = resolve(import.meta.dirname, '..');
const compose = ['compose', '-p', 'full-stack-file-vault-application', '-f', 'compose.application.yaml'];
const helperImage = 'golang:1.27.1-bookworm';
export async function docker(args, outputFile) {
 const child = spawn('docker', args, { cwd: root, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
 // Diagnostics may include credentials. Report only the failed operation.
 child.stderr.resume();
 let output = '';
 const stream = outputFile ? pipeline(child.stdout, createWriteStream(outputFile, { flags: 'wx', mode: 0o600 })) :
  new Promise((done, fail) => {
   child.stdout.on('data', chunk => { output += chunk; if (output.length > 1048576) { child.kill(); fail(new Error('Command output exceeded limit.')); } });
   child.stdout.on('end', done); child.stdout.on('error', fail);
  });
 const exit = new Promise((done, fail) => {
  child.on('error', fail);
  child.on('close', code => code === 0 ? done() : fail(new Error('Backup Docker operation failed: ' + args[0])));
 });
 await Promise.all([stream, exit]); return output.trim();
}
export async function backupApplication() {
 const directory = resolve(root, 'tmp/backups', Date.now() + '-' + randomBytes(6).toString('hex'));
 const plain = resolve(directory, 'plaintext');
 await mkdir(plain, { recursive: true, mode: 0o700 });
 const keyPath = resolve(root, '.secrets/backup-key');
 await mkdir(resolve(root, '.secrets'), { recursive: true });
 try { await writeFile(keyPath, randomBytes(32), { flag: 'wx', mode: 0o600 }); }
 catch (error) { if (error.code !== 'EEXIST') throw error; }
 const key = await readFile(keyPath);
 if (key.length !== 32) throw new Error('Backup key must contain exactly 32 bytes.');
 let paused = false;
 let complete = false;
 const encrypted = resolve(directory, 'backup.enc');
 try {
  const api = await docker([...compose, 'ps', '-q', 'api']);
  if (!api) throw new Error('Start the isolated application rehearsal first.');
  const inspection = JSON.parse(await docker(['inspect', api]))[0];
  const volume = inspection.Mounts.find(mount => mount.Destination === '/data');
  if (volume?.Name !== 'full-stack-file-vault-application_application_files') throw new Error('Unexpected file volume; backup refused.');
  paused = true;
  await docker([...compose, 'stop', 'web', 'api', 'collector']);
  for (const service of ['web', 'api', 'collector']) {
   const id = await docker([...compose, 'ps', '-a', '-q', service]);
   if (!id || JSON.parse(await docker(['inspect', id]))[0].State.Running) throw new Error('Writer quiescence could not be verified.');
  }
  await docker([...compose, 'exec', '-T', 'postgres', 'pg_dump', '-U', 'vault_operator', '-d', 'vault', '--format=custom'], resolve(plain, 'database.dump'));
  await docker(['run', '--rm', '--network', 'none', '--mount', 'type=volume,src=' + volume.Name + ',dst=/data,readonly', helperImage, 'tar', '-C', '/data', '-cpf', '-', '.'], resolve(plain, 'blobs.tar'));
  const manifest = { format: 1, createdAt: new Date().toISOString(), apiImage: inspection.Image, consistency: 'all application writers stopped', encryption: 'AES-256-GCM', destination: 'local rehearsal; off-host copy still required' };
  await writeFile(resolve(plain, 'manifest.json'), JSON.stringify(manifest, null, 2), { flag: 'wx', mode: 0o600 });
  await docker([...compose, 'up', '-d', '--no-deps', '--wait', 'api', 'web', 'collector']);
  paused = false;
  const bundle = resolve(plain, 'bundle.tar');
  await docker(['run', '--rm', '--network', 'none', '--mount', 'type=bind,src=' + plain + ',dst=/backup,readonly', helperImage, 'tar', '-C', '/backup', '-cf', '-', 'database.dump', 'blobs.tar', 'manifest.json'], bundle);
  const nonce = randomBytes(12);
  const header = Buffer.concat([Buffer.from('FVBACKUP1'), nonce]);
  const cipher = createCipheriv('aes-256-gcm', key, nonce);
  cipher.setAAD(header);
  await writeFile(encrypted, header, { flag: 'wx', mode: 0o600 });
  await pipeline(createReadStream(bundle), cipher, createWriteStream(encrypted, { flags: 'a' }));
  await appendFile(encrypted, cipher.getAuthTag());
  await writeFile(resolve(directory, 'manifest.json'), JSON.stringify(manifest, null, 2), { flag: 'wx', mode: 0o600 });
  complete = true;
  return { directory, encrypted };
 } finally {
  key.fill(0);
  try { if (paused) await docker([...compose, 'up', '-d', '--no-deps', '--wait', 'api', 'web', 'collector']); }
  finally {
   // Only the newly allocated private plaintext directory can be recursively removed.
   if (!plain.startsWith(directory + sep) || !directory.startsWith(resolve(root, 'tmp/backups') + sep)) throw new Error('Unsafe cleanup path.');
   await rm(plain, { recursive: true, force: true });
   if (!complete) await rm(encrypted, { force: true });
  }
 }
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
 backupApplication().then(result => console.log('Encrypted local backup: ' + result.encrypted))
  .catch(error => { console.error(error.message); process.exitCode = 1; });
}
