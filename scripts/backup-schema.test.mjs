import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validateRestoredState } from './backup-schema.mjs';
const migrations = ['000014_upload_receipts.up.sql', '000025_folders.up.sql', '000026_future.down.sql'];
const valid = { schema_version: 25, schema_dirty: false, quota_mismatches: 0 };
test('accepts a clean current restore and a supported older backup', () => {
 validateRestoredState(valid, migrations);
 validateRestoredState({ ...valid, schema_version: 14 }, migrations);
});
for (const [name, change] of [
 ['dirty migration', { schema_dirty: true }],
 ['missing dirty state', { schema_dirty: undefined }],
 ['future schema', { schema_version: 26 }],
 ['unsupported old schema', { schema_version: 13 }],
 ['malformed schema', { schema_version: '25' }],
 ['quota mismatch', { quota_mismatches: 1 }],
]) {
 test('rejects ' + name, () => assert.throws(() => validateRestoredState({ ...valid, ...change }, migrations)));
}