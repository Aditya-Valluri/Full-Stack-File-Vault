import { defineConfig } from '@playwright/test';
export default defineConfig({
 testDir: './tests', testMatch: '**/*.spec.ts',
 globalSetup: './tests/setup.ts',
 fullyParallel: false, workers: 1, retries: 0, timeout: 120_000,
 expect: { timeout: 15_000 },
 reporter: [['list'], ['html', { open: 'never' }]],
 use: { baseURL: 'http://127.0.0.1:4173', browserName: 'chromium', trace: 'off', screenshot: 'only-on-failure' },
});
