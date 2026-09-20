/** Format byte strings without losing bigint precision. */
export function formatBytes(value: string | number | bigint): string {
 const bytes = BigInt(value);
 if (bytes < 1000n) return `${bytes} B`;
 const units = ['KB', 'MB', 'GB', 'TB', 'PB', 'EB'];
 let unit = 1000n;
 let i = 0;
 while (bytes >= unit * 1000n && i < units.length - 1) { unit *= 1000n; i++; }
 const tenths = bytes * 10n / unit;
 return `${tenths / 10n}.${tenths % 10n} ${units[i]}`;
}
export function formatDate(value: string): string {
 return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value));
}
export function quotaPercent(used: string, total: string): number {
 const capacity = BigInt(total);
 return capacity === 0n ? 0 : Math.min(100, Number(BigInt(used) * 100n / capacity));
}
