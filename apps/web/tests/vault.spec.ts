import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { readFile } from 'node:fs/promises';

async function login(page: Page, name: string) {
 await page.goto('/');
 await page.getByLabel('Username', { exact: true }).fill(name);
 await page.getByLabel('Password', { exact: true }).fill(process.env.E2E_PASSWORD!);
 await page.getByRole('button', { name: 'Sign in', exact: true }).click();
 await expect(page.getByRole('heading', { name: 'My files', exact: true })).toBeVisible();
 await expect(page.getByText('Loading your files…')).not.toBeVisible();
}

test('files, dedup quota, sharing, revocation, and audited administration', async ({ page, browser }) => {
 await login(page, 'e2e.owner');
 await page.getByRole('button', { name: 'Upload files', exact: true }).click();
 const bytes = Buffer.from('Browser-tested vault content.');
 await page.getByLabel('Select files to upload').setInputFiles([
  { name: 'project-notes.txt', mimeType: 'text/plain', buffer: bytes },
  { name: 'notes-copy.txt', mimeType: 'text/plain', buffer: bytes },
 ]);
 await page.getByRole('button', { name: 'Upload selected files' }).click();
 await expect(page.getByText('Uploaded 2 files.', { exact: true })).toBeVisible();
 await expect(page.getByRole('button', { name: 'Share project-notes.txt', exact: true })).toBeVisible();
 await expect(page.getByText('50.00% less unique content', { exact: false })).toBeVisible();
 const download = page.waitForEvent('download');
 await page.getByRole('button', { name: 'Download project-notes.txt', exact: true }).click();
 const downloaded = await download;
 expect(await readFile((await downloaded.path())!)).toEqual(bytes);
 await page.getByLabel('Search filenames').fill('project');
 await page.getByRole('button', { name: 'Search', exact: true }).click();
 await expect(page.getByRole('button', { name: 'Share notes-copy.txt', exact: true })).not.toBeVisible();
 await page.getByLabel('Search filenames').fill('');
 await page.getByRole('button', { name: 'Search', exact: true }).click();
 await expect(page.getByRole('button', { name: 'Share notes-copy.txt', exact: true })).toBeVisible();
 await page.getByRole('button', { name: 'Share project-notes.txt', exact: true }).click();
 await page.getByRole('button', { name: 'Create sharing link', exact: true }).click();
 const link = await page.getByLabel('Your new link', { exact: true }).inputValue();
 expect(link).toContain('/share#');
 const guestContext = await browser.newContext();
 const guest = await guestContext.newPage();
 try {
  await guest.goto(link);
  await expect(guest.getByRole('heading', { name: 'project-notes.txt', exact: true })).toBeVisible();
  expect(guest.url()).not.toContain('#');
  const sharedDownload = guest.waitForEvent('download');
  await guest.getByRole('button', { name: 'Download file', exact: true }).click();
  expect(await readFile((await (await sharedDownload).path())!)).toEqual(bytes);
  await page.getByRole('button', { name: /^Revoke link ending/ }).first().click();
  await expect(page.getByText('No active sharing links.')).toBeVisible();
  await guest.goto(link);
  await expect(guest.getByRole('heading', { name: 'This link is unavailable' })).toBeVisible();
 } finally { await guestContext.close(); }
 await page.getByRole('button', { name: 'Close dialog', exact: true }).click();
 await page.getByRole('button', { name: 'Delete notes-copy.txt', exact: true }).click();
 await page.getByRole('button', { name: 'Delete file', exact: true }).click();
 await expect(page.getByRole('button', { name: 'Delete notes-copy.txt', exact: true })).not.toBeVisible();

 const adminContext = await browser.newContext();
 const admin = await adminContext.newPage();
 try {
  await login(admin, 'e2e.admin');
  await admin.getByRole('button', { name: 'Administration', exact: true }).click();
  await expect(admin.getByRole('heading', { name: 'Administration', exact: true })).toBeVisible();
  await admin.getByRole('button', { name: 'Edit quota for e2e.owner', exact: true }).click();
  await admin.getByLabel('Quota in bytes', { exact: true }).fill('1');
  await admin.getByRole('button', { name: 'Save quota', exact: true }).click();
  await expect(admin.getByRole('dialog').getByRole('alert')).toContainText('Quota must cover existing files');
  await admin.getByLabel('Quota in bytes', { exact: true }).fill('20000000');
  await admin.getByRole('button', { name: 'Save quota', exact: true }).click();
  await expect(admin.getByText('Storage quota updated and recorded in the audit log.')).toBeVisible();
  const owner = admin.getByRole('row').filter({ hasText: 'e2e.owner' });
  await owner.getByRole('button', { name: 'Disable', exact: true }).click();
  await admin.getByRole('button', { name: 'Disable account', exact: true }).click();
  await expect(owner.getByText('Disabled', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Refresh files', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Sign in to your vault' })).toBeVisible();
  await owner.getByRole('button', { name: 'Enable', exact: true }).click();
  await admin.getByRole('button', { name: 'Enable account', exact: true }).click();
  await expect(owner.getByText('Active', { exact: true })).toBeVisible();
  await admin.getByRole('tab', { name: 'Audit log', exact: true }).click();
  await expect(admin.getByText('Quota changed', { exact: true })).toBeVisible();
  await expect(admin.getByText('Account disabled', { exact: true })).toBeVisible();
 } finally { await adminContext.close(); }
});

test('keyboard-accessible dashboard and mobile layout', async ({ page }, testInfo) => {
 await login(page, 'e2e.recipient');
 await page.getByRole('button', { name: 'Upload files', exact: true }).click();
 await page.getByLabel('Select files to upload').setInputFiles({ name: 'mobile-layout.txt', mimeType: 'text/plain', buffer: Buffer.from('Accessible table content') });
 await page.getByRole('button', { name: 'Upload selected files' }).click();
 await expect(page.getByText('Uploaded 1 file.', { exact: true })).toBeVisible();
 await expect(page.getByRole('button', { name: 'Download mobile-layout.txt', exact: true })).toBeVisible();
 const desktop = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
 expect(desktop.violations).toEqual([]);
 await page.screenshot({ path: testInfo.outputPath('vault-desktop.png'), fullPage: true });
 await page.setViewportSize({ width: 390, height: 844 });
 await expect(page.getByRole('button', { name: 'Upload files', exact: true })).toBeVisible();
 const layout = await page.evaluate(() => ({ width: window.innerWidth, scroll: document.documentElement.scrollWidth, overflow: [...document.querySelectorAll('*')].filter(node => node.getBoundingClientRect().right > window.innerWidth).map(node => ({ tag: node.tagName, class: node.className, right: node.getBoundingClientRect().right })) })); if (layout.scroll > layout.width) console.log(JSON.stringify(layout)); expect(layout).toMatchObject({ width: 390, scroll: 390 });
 const mobile = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
 expect(mobile.violations).toEqual([]);
 await page.screenshot({ path: testInfo.outputPath('vault-mobile.png'), fullPage: true });
 await page.getByRole('button', { name: 'Upload files', exact: true }).click();
 await expect(page.getByRole('dialog')).toBeVisible();
 await page.keyboard.press('Escape');
 await expect(page.getByRole('dialog')).not.toBeVisible();
 await expect(page.getByRole('button', { name: 'Upload files', exact: true })).toBeFocused();
});

test('single-file image preview and sign-out', async ({ page }) => {
 await page.goto('/');
 await expect(page.getByRole('heading', { name: 'Sign in to your vault' })).toBeVisible();
 expect((await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze()).violations).toEqual([]);
 await login(page, 'e2e.recipient');
 await page.getByRole('button', { name: 'Upload files', exact: true }).click();
 await page.getByLabel('Select files to upload').setInputFiles({
  name: 'preview.png', mimeType: 'image/png',
  buffer: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a0X0AAAAASUVORK5CYII=', 'base64'),
 });
 await page.getByRole('button', { name: 'Upload selected files' }).click();
 await expect(page.getByText('Uploaded 1 file.', { exact: true })).toBeVisible();
 await page.getByRole('button', { name: 'Preview preview.png', exact: true }).click();
 const image = page.getByRole('img', { name: 'preview.png', exact: true });
 await expect(image).toBeVisible();
 await expect.poll(() => image.evaluate(node => (node as HTMLImageElement).naturalWidth)).toBe(1);
 await page.keyboard.press('Escape');
 await expect(page.getByRole('button', { name: 'Preview preview.png', exact: true })).toBeFocused();
 await page.getByRole('button', { name: 'Sign out', exact: true }).click();
 await expect(page.getByRole('heading', { name: 'Sign in to your vault' })).toBeVisible();
});

test('lost upload response can be retried without duplicate files', async ({ page }) => {
 await login(page, 'e2e.retry');
 let dropResponse = true;
 await page.route('**/graphql', async route => {
  if (dropResponse && route.request().postData()?.includes('"operationName":"UploadOne"')) {
   dropResponse = false;
   await route.fetch(); // Let the server commit, then deliberately lose its response.
   await route.abort('failed');
  } else await route.continue();
 });
 await page.getByRole('button', { name: 'Upload files', exact: true }).click();
 await page.getByLabel('Select files to upload').setInputFiles({ name: 'retry-once.txt', mimeType: 'text/plain', buffer: Buffer.from('one') });
 await page.getByRole('button', { name: 'Upload selected files' }).click();
 await expect(page.getByRole('button', { name: 'Retry upload safely', exact: true })).toBeVisible();
 await page.getByRole('button', { name: 'Retry upload safely', exact: true }).click();
 await expect(page.getByText('Uploaded 1 file.', { exact: true })).toBeVisible();
 await expect(page.getByRole('button', { name: 'Download retry-once.txt', exact: true })).toHaveCount(1);
 await expect(page.getByText('1 files shown', { exact: true })).toBeVisible();
 await expect(page.locator('.stat-card').filter({ hasText: 'Storage used' }).locator('strong')).toContainText('3 B');
});
