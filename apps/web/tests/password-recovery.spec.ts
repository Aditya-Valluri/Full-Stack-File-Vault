import { test, expect } from './fixtures';
import { readFile } from 'node:fs/promises';

async function capturedCode(subject: string): Promise<string> {
 try {
  const message = await readFile(process.env.E2E_MAIL_PATH!, 'utf8');
  if (!message.includes(`Subject: ${subject}`)) return '';
  return message.match(/\b[0-9]{7}\b/)?.[0] ?? '';
 } catch { return ''; }
}

test('password change and email reset revoke access and require fresh credentials', async ({ page }) => {
 // Rejected old passwords are covered by the DB integration suite; keep this journey within the five-attempt identity budget.
 const email = `recovery-${Date.now()}@example.test`;
 const initialPassword = 'a long registration password';
 const changedPassword = 'orchard velvet lantern compass';
 const resetPassword = 'violet harbor lantern meadow';

 await page.goto('/');
 await page.getByRole('button', { name: 'New user? Create account', exact: true }).click();
 await page.getByLabel('Email', { exact: true }).fill(email);
 await page.getByRole('button', { name: 'Send verification code', exact: true }).click();
 await expect(page.getByRole('heading', { name: 'Check your email' })).toBeVisible();
 await page.getByRole('button',{name:'I received a registration code',exact:true}).click();
 await expect.poll(() => capturedCode('Verify your Full Stack File Vault email')).toHaveLength(7);
 await page.getByLabel('Verification code (7 digits)', { exact: true }).fill(await capturedCode('Verify your Full Stack File Vault email'));
 await page.getByLabel('New password', { exact: true }).fill(initialPassword);
 await page.getByLabel('Confirm password', { exact: true }).fill(initialPassword);
 await page.getByRole('button', { name: 'Verify and create account', exact: true }).click();
 await expect(page.getByRole('heading', { name: 'My files', exact: true })).toBeVisible();

 await page.getByRole('button', { name: 'Account security', exact: true }).click();
 await page.getByLabel('Current password', { exact: true }).fill(initialPassword);
 await page.getByLabel('New password', { exact: true }).fill(changedPassword);
 await page.getByLabel('Confirm password', { exact: true }).fill(changedPassword);
 await page.getByRole('button', { name: 'Change password', exact: true }).click();
 await expect(page.getByLabel('Username or email', { exact: true })).toBeVisible();

 await page.getByLabel('Username or email', { exact: true }).fill(email);
 await page.getByLabel('Password', { exact: true }).fill(changedPassword);
 await page.getByRole('button', { name: 'Sign in', exact: true }).click();
 await expect(page.locator('.account-name')).toHaveText(email);
 await page.getByRole('button', { name: 'Sign out', exact: true }).click();

 await page.getByRole('button', { name: 'Forgot password?', exact: true }).click();
 await page.getByLabel('Email', { exact: true }).fill(email);
 await page.getByRole('button', { name: 'Send reset code', exact: true }).click();
 await expect.poll(() => capturedCode('Reset your Full Stack File Vault password')).toHaveLength(7);
 await page.getByLabel('Reset code (7 digits)', { exact: true }).fill(await capturedCode('Reset your Full Stack File Vault password'));
 await page.getByLabel('New password', { exact: true }).fill(resetPassword);
 await page.getByLabel('Confirm password', { exact: true }).fill(resetPassword);
 await page.getByRole('button', { name: 'Reset password', exact: true }).click();
 await expect(page.getByRole('heading', { name: 'Password updated' })).toBeVisible();
 await page.getByRole('button', { name: 'Back to sign in', exact: true }).click();

 await page.getByLabel('Username or email', { exact: true }).fill(email);
 await page.getByLabel('Password', { exact: true }).fill(resetPassword);
 await page.getByRole('button', { name: 'Sign in', exact: true }).click();
 await expect(page.locator('.account-name')).toHaveText(email);
});