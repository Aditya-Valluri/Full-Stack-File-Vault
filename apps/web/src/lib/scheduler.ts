/** Serialize admission across GraphQL and browser byte starts. The server remains
 * authoritative across tabs/replicas; this only prevents avoidable local bursts. */
export class RequestScheduler {
 private tail: Promise<unknown> = Promise.resolve();
 private lastStart = -Infinity;
 constructor(private readonly spacingMs = 650) {}
 run<T>(work: () => Promise<T>, signal?: AbortSignal): Promise<T> {
  const pending = this.tail.then(async () => {
   if (signal?.aborted) throw new DOMException('Cancelled', 'AbortError');
   const wait = Math.max(0, this.spacingMs - (performance.now() - this.lastStart));
   if (wait) await new Promise(resolve => setTimeout(resolve, wait));
   if (signal?.aborted) throw new DOMException('Cancelled', 'AbortError');
   this.lastStart = performance.now();
   return work();
  });
  this.tail = pending.catch(() => undefined);
  return pending;
 }
}
export const requests = new RequestScheduler();
