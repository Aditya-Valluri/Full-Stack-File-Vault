// Only schemas with upload receipts (14+) are supported by this verifier.
// Match an actual checked-in migration, reject dirty/unknown states, and retain
// old-backup verification without silently treating it as the latest schema.
export function validateRestoredState(state, migrationFiles) {
 const versions = new Set(migrationFiles.flatMap(name => {
  const match = /^([0-9]{6})_.+\.up\.sql$/.exec(name);
  return match ? [Number(match[1])] : [];
 }));
 if (!Number.isSafeInteger(state.schema_version) || state.schema_version < 14 ||
     !versions.has(state.schema_version) || state.schema_dirty !== false ||
     state.quota_mismatches !== 0) {
  throw new Error('Restored schema or quota invariant failed.');
 }
}