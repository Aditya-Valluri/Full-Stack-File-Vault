import { test, expect } from '@playwright/test';
import { readFile } from 'node:fs/promises';

test('verified email registration rotates the session and supports subsequent email login', async ({ page }) => {
 const email = 'browser-owner@example.test';
 const password = 'browser verification password';
 await page.goto('/');
 await page.getByRole('button', { name: 'New user? Create account', exact: true }).click();
 await page.getByLabel('Email', { exact: true }).fill(email);
 await page.getByRole('button', { name: 'Send verification code', exact: true }).click();
 await expect(page.getByRole('heading', { name: 'Verify your email' })).toBeVisible();
 let code = '';
 await expect.poll(async () => {
  try {
   const message = await readFile(process.env.E2E_MAIL_PATH!, 'utf8');
   code = message.match(/\b[0-9]{7}\b/)?.[0] ?? '';
   return code.length;
  } catch { return 0; }
 }).toBe(7);
 await page.getByLabel('Verification code (7 digits)', { exact: true }).fill(code);
 await page.getByLabel('New password', { exact: true }).fill('Password123456789!');
 await page.getByLabel('Confirm password', { exact: true }).fill('Password123456789!');
 await page.getByRole('button', { name: 'Verify and create account', exact: true }).click();
 await expect(page.getByText('Use at least 15 characters. Avoid common passwords, repeated patterns, and obvious sequences. Try a unique passphrase of unrelated words.', { exact: true })).toBeVisible();
 await page.getByLabel('New password', { exact: true }).fill(password);
 await page.getByLabel('Confirm password', { exact: true }).fill(password);
 await page.getByRole('button', { name: 'Verify and create account', exact: true }).click();
 await expect(page.getByRole('heading', { name: 'My files', exact: true })).toBeVisible();
 await expect(page.locator('.account-name')).toHaveText(email);
 await page.reload();
 await expect(page.locator('.account-name')).toHaveText(email);
 await page.getByRole('button', { name: 'Sign out', exact: true }).click();
 await page.getByLabel('Username or email', { exact: true }).fill(email.toUpperCase());
 await page.getByLabel('Password', { exact: true }).fill(password);
 await page.getByRole('button', { name: 'Sign in', exact: true }).click();
 await expect(page.locator('.account-name')).toHaveText(email);
 expect(await page.evaluate(() => Object.keys(localStorage))).toEqual([]);
});
