import { test, expect } from './fixtures';

test('individual upload progress, transactional tags, and retry of only an unfinished file', async ({ page }) => {
 await page.goto('/');
 await page.getByLabel('Username or email', { exact: true }).fill('e2e.progress');
 await page.getByLabel('Password', { exact: true }).fill(process.env.E2E_PASSWORD!);
 await page.getByRole('button', { name: 'Sign in', exact: true }).click();
 await expect(page.getByRole('heading', { name: 'My files', exact: true })).toBeVisible();
 let dropSecond = true;
 const keys: string[] = [];
 await page.route('**/graphql', async route => {
  const body = route.request().postData() ?? '';
  if (!body.includes('"operationName":"UploadOne"')) { await route.continue(); return; }
  keys.push(body.match(/"idempotencyKey":"([^"]+)"/)?.[1] ?? '');
  if (body.includes('second-progress.txt') && dropSecond) {
   dropSecond = false;
   await route.fetch();
   await new Promise(resolve => setTimeout(resolve, 500));
   await route.abort('failed');
  } else await route.continue();
 });
 await page.getByRole('button', { name: 'Upload files', exact: true }).click();
 await page.getByRole('combobox', { name: 'Upload mode', exact: true }).selectOption('individual');
 await page.getByLabel('Upload tags separated by commas', { exact: true }).fill(' Audit, project, audit ');
 await page.getByLabel('Select files to upload').setInputFiles([
  { name: 'first-progress.txt', mimeType: 'text/plain', buffer: Buffer.from('first progress file') },
  { name: 'second-progress.txt', mimeType: 'text/plain', buffer: Buffer.from('second progress file') },
 ]);
 await page.getByRole('button', { name: 'Upload selected files', exact: true }).click();
 await expect(page.getByRole('button', { name: 'Retry upload safely', exact: true })).toBeVisible();
 await expect(page.getByRole('dialog').getByText('Saved', { exact: true })).toHaveCount(1);
 await page.getByRole('button', { name: 'Retry upload safely', exact: true }).click();
 await expect(page.getByRole('dialog').getByText('Saved', { exact: true })).toHaveCount(2);
 expect(keys).toHaveLength(3);
 expect(keys[0]).not.toBe(keys[1]);
 expect(keys[1]).toBe(keys[2]);
 await page.getByRole('button', { name: 'Close dialog', exact: true }).click();
 await expect(page.getByRole('button', { name: 'Download first-progress.txt', exact: true })).toHaveCount(1);
 await expect(page.getByRole('button', { name: 'Download second-progress.txt', exact: true })).toHaveCount(1);
 await expect(page.getByLabel('Private tags for first-progress.txt', { exact: true })).toHaveText('audit, project');
 await expect(page.getByLabel('Private tags for second-progress.txt', { exact: true })).toHaveText('audit, project');
 await expect(page.getByText('2 files shown', { exact: true })).toBeVisible();
});
