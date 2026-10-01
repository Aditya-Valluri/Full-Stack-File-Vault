import { test, expect, type Page } from './fixtures';
import { createHmac } from 'node:crypto';

function previousTOTP(secret: string): string {
 const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
 let bits = '';
 for (const char of secret) bits += alphabet.indexOf(char).toString(2).padStart(5, '0');
 const bytes = Buffer.from(bits.match(/.{8}/g)!.map(byte => parseInt(byte, 2)));
 const counter = Buffer.alloc(8);
 counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000) - 1));
 const digest = createHmac('sha1', bytes).update(counter).digest();
 const offset = digest[digest.length - 1] & 15;
 return String((digest.readUInt32BE(offset) & 0x7fffffff) % 1000000).padStart(6, '0');
}
async function passwordLogin(page: Page) {
 await page.getByLabel('Username or email', { exact: true }).fill('e2e.mfa');
 await page.getByLabel('Password', { exact: true }).fill(process.env.E2E_PASSWORD!);
 await page.getByRole('button', { name: 'Sign in', exact: true }).click();
}
test('MFA QR enrollment, session revocation, one-use recovery and disable', async ({ page }) => {
 await page.goto('/');
 await passwordLogin(page);
 await expect(page.getByRole('heading', { name: 'My files', exact: true })).toBeVisible();
 await page.getByRole('button', { name: 'Account security', exact: true }).click();
 await page.getByLabel('Current password for MFA', { exact: true }).fill(process.env.E2E_PASSWORD!);
 await page.getByRole('button', { name: 'Set up authenticator', exact: true }).click();
 await expect(page.getByRole('region', { name: 'Authenticator security' }).locator('svg')).toBeVisible();
 const seed = await page.getByLabel('Manual setup key', { exact: true }).inputValue();
 await page.getByLabel('Authenticator code (6 digits)', { exact: true }).fill(previousTOTP(seed));
 await page.getByRole('button', { name: 'Confirm and enable MFA', exact: true }).click();
 const codesPanel = page.getByRole('region', { name: 'MFA recovery codes' });
 await expect(codesPanel).toBeVisible();
 const codes = await codesPanel.locator('code').allTextContents();
 expect(codes).toHaveLength(8);
 expect(new Set(codes).size).toBe(8);
 await page.getByRole('button', { name: 'I saved my codes — sign in', exact: true }).click();
 await passwordLogin(page);
 await expect(page.getByLabel('Authenticator or recovery code', { exact: true })).toBeVisible();
 await expect(page.getByRole('heading', { name: 'My files', exact: true })).toHaveCount(0);
 await page.getByLabel('Authenticator or recovery code', { exact: true }).fill(codes[0]);
 await page.getByRole('button', { name: 'Sign in', exact: true }).click();
 await expect(page.getByRole('heading', { name: 'My files', exact: true })).toBeVisible();
 await page.getByRole('button', { name: 'Sign out', exact: true }).click();
 await passwordLogin(page);
 await page.getByLabel('Authenticator or recovery code', { exact: true }).fill(codes[0]);
 await page.getByRole('button', { name: 'Sign in', exact: true }).click();
 await expect(page.getByText('The username, email or password was not accepted.', { exact: true })).toBeVisible();
 await page.getByLabel('Authenticator or recovery code', { exact: true }).fill(codes[1]);
 await page.getByRole('button', { name: 'Sign in', exact: true }).click();
 await expect(page.getByRole('heading', { name: 'My files', exact: true })).toBeVisible();
 await page.getByRole('button', { name: 'Account security', exact: true }).click();
 await page.getByLabel('Current password for MFA', { exact: true }).fill(process.env.E2E_PASSWORD!);
 await page.getByLabel('Authenticator or recovery code', { exact: true }).fill(codes[2]);
 await page.getByRole('button', { name: 'Disable MFA', exact: true }).click();
 await expect(page.getByRole('heading', { name: 'Sign in to your vault' })).toBeVisible();
 await passwordLogin(page);
 await expect(page.getByRole('heading', { name: 'My files', exact: true })).toBeVisible();
 expect(await page.evaluate(() => Object.keys(localStorage))).toEqual([]);
 expect(await page.evaluate(() => Object.keys(sessionStorage))).toEqual([]);
});
