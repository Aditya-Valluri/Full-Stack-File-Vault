import { test, expect } from './fixtures';

test('view-only sharing previews safely on mobile and revocation removes access', async ({ page, browser }) => {
 await page.goto('/');
 await page.getByLabel('Username or email', { exact: true }).fill('e2e.progress');
 await page.getByLabel('Password', { exact: true }).fill(process.env.E2E_PASSWORD!);
 await page.getByRole('button', { name: 'Sign in', exact: true }).click();
 await expect(page.getByRole('heading', { name: 'My files', exact: true })).toBeVisible();
 await page.getByRole('button', { name: 'Upload files', exact: true }).click();
 await page.getByLabel('Select files to upload').setInputFiles({ name: 'view-only.txt', mimeType: 'text/plain', buffer: Buffer.from('Authorized view-only preview.') });
 await page.getByRole('button', { name: 'Upload selected files' }).click();
 await expect(page.getByText('Uploaded 1 file.', { exact: true })).toBeVisible();
 await page.getByRole('button', { name: 'Share view-only.txt', exact: true }).click();
 await page.getByRole('combobox', { name: 'Permission', exact: true }).selectOption('PREVIEW_ONLY');
 await page.getByRole('button', { name: 'Create sharing link', exact: true }).click();
 const link = await page.getByLabel('Your new link', { exact: true }).inputValue();
 const context = await browser.newContext({ viewport: { width: 390, height: 844 } });
 const guest = await context.newPage();
 try {
  await guest.goto(link);
  await expect(guest.getByRole('heading', { name: 'view-only.txt', exact: true })).toBeVisible();
  await expect(guest.getByRole('button', { name: 'Download file', exact: true })).toHaveCount(0);
  const previewResponse = guest.waitForResponse(response => new URL(response.url()).pathname.startsWith('/shared-content/'));
  await guest.getByRole('button', { name: 'Preview', exact: true }).click();
  expect((await previewResponse).status()).toBe(200);
  await expect(guest.locator('.text-preview')).toHaveText('Authorized view-only preview.');
  expect(await guest.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.getByRole('button', { name: /^Revoke link ending/ }).click();
  await expect(page.getByText('No active sharing links.', { exact: true })).toBeVisible();
  await guest.goto(link);
  await expect(guest.getByRole('heading', { name: 'This link is unavailable', exact: true })).toBeVisible();
 } finally { await context.close(); }
});
