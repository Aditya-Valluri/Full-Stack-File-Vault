// Local production-mode rehearsal only: never targets an arbitrary deployment.
import { backupApplication } from './backup-application.mjs';
import { chromium } from '../apps/web/node_modules/playwright/index.mjs';
import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { readFile, mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
const root = resolve(import.meta.dirname, '..');
const compose = ['compose', '-p', 'file-vault-application', '-f', 'compose.application.yaml'];
const loginName = 'smoke.' + randomBytes(8).toString('hex');
const password = randomBytes(24).toString('hex');
function docker(args, input = '') {
 return new Promise((done, fail) => {
  const child = spawn('docker', [...compose, ...args], { cwd: root, windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'] });
  child.stdout.resume(); let diagnostic = ''; child.stderr.on('data', chunk => { diagnostic = (diagnostic + String(chunk)).slice(-2000); });
  child.on('error', () => fail(new Error('Unable to start deployment check.')));
  child.on('exit', code => code === 0 ? done() : fail(new Error('Deployment check ' + args[0] + ' failed: ' + diagnostic.trim())));
  child.stdin.on('error', () => undefined); child.stdin.end(input);
 });
}
let browser;
let provisioned = false;
let filesClean = true;
try {
 await docker(['run', '--rm', '-T', 'provision', '-login', loginName, '-role', 'USER'], password);
 provisioned = true;
 browser = await chromium.launch();
 // Trust bypass is restricted to this generated localhost rehearsal certificate.
 const context = await browser.newContext({ ignoreHTTPSErrors: true });
 const page = await context.newPage();
 const response = await page.goto('https://localhost:8443/');
 if (response.status() !== 200 || !response.headers()['content-security-policy']) throw new Error('Static security headers missing.');
 await page.getByLabel('Username', { exact: true }).fill(loginName);
 await page.getByLabel('Password', { exact: true }).fill(password);
 await page.getByRole('button', { name: 'Sign in', exact: true }).click();
 await page.getByRole('heading', { name: 'My files', exact: true }).waitFor();
 const cookies = await context.cookies();
 if (!cookies.some(cookie => cookie.name === '__Host-vault_session' && cookie.secure && cookie.httpOnly && cookie.sameSite === 'Lax' && cookie.path === '/')) throw new Error('Production session cookie is not hardened.');
 await page.getByRole('button', { name: 'Upload files', exact: true }).click();
 const bytes = Buffer.from('Production container transport check.');
 await page.getByLabel('Select files to upload').setInputFiles({ name: 'container-check.txt', mimeType: 'text/plain', buffer: bytes });
 filesClean = false;
 await page.getByRole('button', { name: 'Upload selected files' }).click();
 await page.getByText('Uploaded 1 file.', { exact: true }).waitFor();
 await page.getByRole('button', { name: 'Edit tags for container-check.txt', exact: true }).click();
 await page.getByLabel('Tags separated by commas').fill('local-smoke, private');
 await page.getByRole('button', { name: 'Save tags', exact: true }).click();
 await page.getByText('Private tags saved.', { exact: true }).waitFor();
 await page.getByLabel('Private tags for container-check.txt').waitFor();
 if (await page.getByLabel('Private tags for container-check.txt').textContent() !== 'local-smoke, private') throw new Error('Private tag persistence failed.');
 const downloadEvent = page.waitForEvent('download');
 await page.getByRole('button', { name: 'Download container-check.txt', exact: true }).click();
 const download = await downloadEvent;
 if (!(await readFile(await download.path())).equals(bytes)) throw new Error('Downloaded bytes differ.');
 await mkdir(resolve(root, 'tmp'), { recursive: true });
 await page.screenshot({ path: resolve(root, 'tmp/deployment-dashboard.png'), fullPage: true });
 if (process.argv.includes('--backup')) {
  const backup = await backupApplication();
  await new Promise((done, fail) => {
   const child = spawn(process.execPath, ['scripts/verify-backup.mjs', backup.encrypted], { cwd: root, windowsHide: true, stdio: 'inherit' });
   child.on('error', fail); child.on('close', code => code === 0 ? done() : fail(new Error('Backup restoration failed.')));
  });
 }
 await page.getByRole('button', { name: 'Delete container-check.txt', exact: true }).click();
 await page.getByRole('button', { name: 'Delete file', exact: true }).click();
 await page.getByText('File deleted. Its storage quota has been released.', { exact: true }).waitFor();
 filesClean = true;
 await page.getByRole('button', { name: 'Sign out', exact: true }).click();
 await page.getByRole('heading', { name: 'Sign in to your vault' }).waitFor();
 console.log('PASS: production-mode TLS, CSP, secure cookie, upload, private tags, download, delete, and sign-out.');
} finally {
 if (browser) await browser.close();
 if (provisioned && filesClean) {
  // The generated ASCII name identifies only this test account. Files were
  // deleted through the application before removing the fixture account.
  await docker(['exec', '-T', 'postgres', 'psql', '-v', 'ON_ERROR_STOP=1', '-U', 'vault_operator', '-d', 'vault'],
   "BEGIN; DELETE FROM vault.sessions WHERE user_id IN (SELECT user_id FROM vault.credentials WHERE login_name='" + loginName + "'); WITH removed AS (DELETE FROM vault.credentials WHERE login_name='" + loginName + "' RETURNING user_id) DELETE FROM vault.users WHERE id IN (SELECT user_id FROM removed); COMMIT;\n");
 }
}
