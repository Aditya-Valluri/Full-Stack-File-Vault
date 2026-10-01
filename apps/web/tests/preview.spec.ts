import { test, expect } from './fixtures';

function samplePDF(): Buffer {
 const objects = [
  '<< /Type /Catalog /Pages 2 0 R /OpenAction 6 0 R >>',
  '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
  '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>',
  '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  '<< /Length 45 >>\nstream\nBT /F1 18 Tf 20 100 Td (Safe PDF preview) Tj ET\nendstream',
  '<< /S /JavaScript /JS (app.alert("must not execute")) >>',
 ];
 let pdf = '%PDF-1.4\n';
 const offsets = [0];
 for (const [index, object] of objects.entries()) { offsets.push(Buffer.byteLength(pdf)); pdf += `${index + 1} 0 obj\n${object}\nendobj\n`; }
 const xref = Buffer.byteLength(pdf);
 pdf += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`;
 pdf += offsets.slice(1).map(offset => String(offset).padStart(10, '0') + ' 00000 n \n').join('');
 pdf += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
 return Buffer.from(pdf);
}
test('authorized PDF/TXT previews, inert active content, and metadata-only details', async ({ page }) => {
 await page.goto('/');
 await page.getByLabel('Username or email', { exact: true }).fill('e2e.recipient');
 await page.getByLabel('Password', { exact: true }).fill(process.env.E2E_PASSWORD!);
 await page.getByRole('button', { name: 'Sign in', exact: true }).click();
 await expect(page.getByRole('heading', { name: 'My files', exact: true })).toBeVisible();
 await page.getByRole('button', { name: 'Upload files', exact: true }).click();
 await page.getByLabel('Select files to upload').setInputFiles([
  { name: 'passive-preview.pdf', mimeType: 'application/pdf', buffer: samplePDF() },
  { name: 'passive-preview.txt', mimeType: 'text/plain', buffer: Buffer.from('Ordinary text\n<script>window.previewExecuted = true</script>') },
  { name: 'active-preview.html', mimeType: 'text/html', buffer: Buffer.from('<html><script>alert(1)</script></html>') },
  { name: 'active-preview.js', mimeType: 'text/plain', buffer: Buffer.from('alert("not executed")') },
 ]);
 await page.getByRole('button', { name: 'Upload selected files' }).click();
 await expect(page.getByText('Uploaded 4 files.', { exact: true })).toBeVisible();
 await expect(page.getByRole('button', { name: 'Preview active-preview.html', exact: true })).toHaveCount(0);
 await expect(page.getByRole('button', { name: 'Preview active-preview.js', exact: true })).toHaveCount(0);
 await page.getByRole('button', { name: 'Details for passive-preview.pdf', exact: true }).click();
 await expect(page.getByRole('dialog')).toContainText('application/pdf');
 await expect(page.getByRole('button', { name: 'Create sharing link', exact: true })).toHaveCount(0);
 await expect(page.getByRole('dialog').locator('canvas,img,iframe')).toHaveCount(0);
 await page.getByRole('button', { name: 'Close dialog', exact: true }).click();
 let dialogs = 0;
 page.on('dialog', async dialog => { dialogs++; await dialog.dismiss(); });
 await page.getByRole('button', { name: 'Preview passive-preview.pdf', exact: true }).click();
 await expect(page.getByRole('dialog').locator('canvas')).toBeVisible({ timeout: 20000 });
 await expect(page.getByText('Page 1 of 1', { exact: true })).toBeVisible();
 expect(dialogs).toBe(0);
 await expect(page.getByRole('dialog').locator('iframe,object,embed')).toHaveCount(0);
 await page.setViewportSize({ width: 390, height: 844 });
 expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
 await page.getByRole('button', { name: 'Close dialog', exact: true }).click();
 await page.getByRole('button', { name: 'Preview passive-preview.txt', exact: true }).click();
 await expect(page.locator('.text-preview')).toContainText('<script>window.previewExecuted = true</script>');
 expect(await page.evaluate(() => 'previewExecuted' in window)).toBe(false);
});
