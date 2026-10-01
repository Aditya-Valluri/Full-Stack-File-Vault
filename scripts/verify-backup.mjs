// Restores a generated encrypted rehearsal archive into disposable PostgreSQL.
// Does not replace the application database or claim a full application recovery drill.
import { spawn } from 'node:child_process';
import { createReadStream, createWriteStream } from 'node:fs';
import { open, readFile, writeFile, mkdir, rm, copyFile, readdir } from 'node:fs/promises';
import { createDecipheriv, createHash, randomBytes } from 'node:crypto';
import { pipeline } from 'node:stream/promises';
import { resolve, sep } from 'node:path';
import { docker } from './backup-application.mjs';
import { validateRestoredState } from './backup-schema.mjs';

const root = resolve(import.meta.dirname, '..');
const archive = resolve(process.argv[2] || '');
const allowed = resolve(root, 'tmp/backups') + sep;
if (!archive.startsWith(allowed) || !archive.endsWith(sep + 'backup.enc')) throw new Error('Select a generated local backup.enc beneath tmp/backups.');
const suffix = randomBytes(8).toString('hex');
const name = 'full-stack-file-vault-restore-' + suffix;
const work = resolve(root, 'tmp', name);
await mkdir(work, { mode: 0o700 });
async function decrypt(source, target) {
 const file = await open(source, 'r');
 const key = await readFile(resolve(root, '.secrets/backup-key'));
 try {
  const size = (await file.stat()).size;
  if (size < 37 || key.length !== 32) throw new Error('Invalid backup.');
  const header = Buffer.alloc(21), tag = Buffer.alloc(16);
  await file.read(header, 0, 21, 0); await file.read(tag, 0, 16, size - 16);
  if (header.subarray(0, 9).toString() !== 'FVBACKUP1') throw new Error('Unknown backup format.');
  const cipher = createDecipheriv('aes-256-gcm', key, header.subarray(9));
  cipher.setAAD(header); cipher.setAuthTag(tag);
  try { await pipeline(createReadStream(source, { start: 21, end: size - 17 }), cipher, createWriteStream(target, { flags: 'wx', mode: 0o600 })); }
  catch (error) { await rm(target, { force: true }); throw error; }
 } finally { key.fill(0); await file.close(); }
}
async function inputCommand(args, input, file = false) {
 const child = spawn('docker', args, { cwd: root, windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'] });
 child.stderr.resume(); let output = '';
 child.stdout.on('data', data => { output += data; });
 const exit = new Promise((done, fail) => { child.on('error', fail); child.on('close', code => code === 0 ? done() : fail(new Error('Restore operation failed: ' + args[0]))); });
 const stream = file ? pipeline(createReadStream(input), child.stdin) : new Promise((done, fail) => { child.stdin.on('error', fail); child.stdin.end(input, done); });
 await Promise.all([exit, stream]); return output.trim();
}
const sql = statement => inputCommand(['exec', '-i', name, 'psql', '-At', '-v', 'ON_ERROR_STOP=1', '-U', 'vault_operator', '-d', 'vault'], statement);
let started = false;
try {
 const bundle = resolve(work, 'bundle.tar');
 await decrypt(archive, bundle);
 const tampered = resolve(work, 'tampered.enc');
 await copyFile(archive, tampered);
 const handle = await open(tampered, 'r+'); const byte = Buffer.alloc(1);
 await handle.read(byte, 0, 1, 30); byte[0] ^= 1; await handle.write(byte, 0, 1, 30); await handle.close();
 let rejected = false;
 try { await decrypt(tampered, resolve(work, 'invalid.tar')); } catch { rejected = true; }
 if (!rejected) throw new Error('Tamper detection failed.');
 const helper = ['run', '--rm', '--network', 'none', '--mount', 'type=bind,src=' + work + ',dst=/backup', 'golang:1.27.1-bookworm'];
 const members = (await docker([...helper, 'tar', '-tf', '/backup/bundle.tar'])).split('\n').sort();
 if (JSON.stringify(members) !== JSON.stringify(['blobs.tar', 'database.dump', 'manifest.json'])) throw new Error('Unexpected archive members.');
 await docker([...helper, 'tar', '-C', '/backup', '-xf', '/backup/bundle.tar', 'database.dump', 'blobs.tar', 'manifest.json']);
 // No network or published port: trust is restricted to Unix-socket exec in this disposable container.
 await docker(['run', '-d', '--name', name, '--network', 'none', '-e', 'POSTGRES_USER=vault_operator', '-e', 'POSTGRES_DB=vault', '-e', 'POSTGRES_HOST_AUTH_METHOD=trust', 'postgres:17-bookworm']);
 started = true;
 // The temporary initialization server accepts Unix sockets before restart.
 // Wait for TCP readiness so pg_restore cannot race that restart.
 let ready = false;
 for (let attempt = 0; attempt < 30; attempt++) {
  try { await docker(['exec', name, 'pg_isready', '-h', '127.0.0.1', '-U', 'vault_operator', '-d', 'vault']); ready = true; break; }
  catch { await new Promise(done => setTimeout(done, 1000)); }
 }
 if (!ready) throw new Error('Disposable PostgreSQL did not become ready.');
 console.log('[restore] Final PostgreSQL listener ready; creating restricted role prerequisites');
 await sql('CREATE ROLE vault_runtime NOLOGIN; CREATE ROLE vault_gc NOLOGIN;');
 console.log('[restore] Restoring database archive');
 await inputCommand(['exec', '-i', name, 'pg_restore', '--exit-on-error', '-U', 'vault_operator', '-d', 'vault'], resolve(work, 'database.dump'), true);
 console.log('[restore] Checking schema and quota invariants');
 const result = JSON.parse(await sql("SELECT json_build_object('schema_version',(SELECT version FROM public.schema_migrations),'schema_dirty',(SELECT dirty FROM public.schema_migrations),'files',(SELECT count(*) FROM vault.files),'receipts',(SELECT count(*) FROM vault.upload_receipts),'quota_mismatches',(SELECT count(*) FROM vault.users u WHERE u.used_bytes <> (SELECT coalesce(sum(b.size_bytes),0) FROM vault.files f JOIN vault.blobs b ON b.id=f.blob_id WHERE f.owner_id=u.id)));"));
 validateRestoredState(result, await readdir(resolve(root, 'db/migrations')));
 const blobs = JSON.parse(await sql("SELECT coalesce(json_agg(json_build_object('key',b.storage_key,'digest',encode(b.sha256,'hex'),'size',b.size_bytes)),'[]'::json) FROM vault.blobs b WHERE EXISTS (SELECT 1 FROM vault.files f WHERE f.blob_id=b.id);"));
 for (const blob of blobs) {
  if (!/^blob-[a-f0-9]{64}$/.test(blob.key)) throw new Error('Unsafe restored storage key.');
  const hash = createHash('sha256'); let size = 0;
  const child = spawn('docker', [...helper, 'tar', '-xOf', '/backup/blobs.tar', './blobs/' + blob.key], { cwd: root, windowsHide: true, stdio: ['ignore','pipe','pipe'] });
  child.stderr.resume();
  child.stdout.on('data', chunk => { size += chunk.length; hash.update(chunk); });
  await new Promise((done, fail) => { child.on('error', fail); child.on('close', code => code === 0 ? done() : fail(new Error('Restored file is missing.'))); });
  if (size !== blob.size || hash.digest('hex') !== blob.digest) throw new Error('Restored file integrity check failed.');
 }
 result.verifiedBlobs = blobs.length;
 await writeFile(resolve(archive, '..', 'restore-result.json'), JSON.stringify({ ...result, testedAt: new Date().toISOString(), tamperRejected: true, scope: 'database restore, referenced blob SHA-256 and archive authentication; browser recovery not yet verified' }, null, 2));
 console.log('PASS: authenticated decryption, tamper rejection, PostgreSQL restore, schema verified, quota consistency. File count: ' + result.files);
} finally {
 try { if (started) await docker(['rm', '-f', '-v', name]); }
 finally {
  if (!work.startsWith(resolve(root, 'tmp') + sep) || !name.startsWith('full-stack-file-vault-restore-')) throw new Error('Unsafe cleanup path.');
  await rm(work, { recursive: true, force: true });
 }
}
