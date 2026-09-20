import { describe, expect, it, vi } from 'vitest';
import { formatBytes, quotaPercent } from './format';
import { RequestScheduler } from './scheduler';

describe('precise storage display', () => {
 it('uses decimal units and preserves large integer ratios', () => {
  expect(formatBytes('10000000')).toBe('10.0 MB');
  expect(formatBytes('0')).toBe('0 B');
  expect(quotaPercent('4503599627370497', '9007199254740994')).toBe(50);
  expect(quotaPercent('0', '0')).toBe(0);
 });
});
describe('request admission pacing', () => {
 it('spaces serialized starts and continues after failures', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'performance'] });
  try {
   const scheduler = new RequestScheduler(650);
   const starts: number[] = [];
   const first = scheduler.run(async () => { starts.push(performance.now()); throw new Error('failure'); }).catch(() => undefined);
   const second = scheduler.run(async () => { starts.push(performance.now()); });
   const third = scheduler.run(async () => { starts.push(performance.now()); });
   await vi.runAllTimersAsync();
   await Promise.all([first, second, third]);
   expect(starts).toEqual([0, 650, 1300]);
  } finally { vi.useRealTimers(); }
 });
 it('does not send an aborted queued request', async () => {
  const scheduler = new RequestScheduler(0);
  const controller = new AbortController(); controller.abort();
  const work = vi.fn();
  await expect(scheduler.run(work, controller.signal)).rejects.toMatchObject({ name: 'AbortError' });
  expect(work).not.toHaveBeenCalled();
 });
});
