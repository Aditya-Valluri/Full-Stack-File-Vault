export type UploadProgress = { loaded: number; total?: number };

/** Same GraphQL multipart endpoint, cookies and CSRF headers as JSON operations.
 * Browser progress measures request bytes (including multipart overhead), not
 * server publication. Completion is reported only after the GraphQL response. */
export function uploadRequest(body: FormData, headers: Headers, signal: AbortSignal,
 onProgress?: (progress: UploadProgress) => void): Promise<Response> {
 return new Promise((resolve, reject) => {
  const xhr = new XMLHttpRequest();
  const abort = () => xhr.abort();
  const clean = () => signal.removeEventListener('abort', abort);
  xhr.open('POST', '/graphql');
  xhr.withCredentials = true;
  xhr.timeout = 120_000;
  headers.forEach((value, key) => xhr.setRequestHeader(key, value));
  xhr.upload.onprogress = event => onProgress?.({ loaded: event.loaded,
   total: event.lengthComputable && event.total > 0 ? event.total : undefined });
  xhr.onload = () => {
   clean();
   if (!xhr.status) { reject(new Error('Upload response unavailable.')); return; }
   resolve(new Response(xhr.responseText, { status: xhr.status,
    headers: { 'content-type': xhr.getResponseHeader('content-type') ?? '' } }));
  };
  xhr.onerror = xhr.ontimeout = () => { clean(); reject(new Error('Upload result unavailable.')); };
  xhr.onabort = () => { clean(); reject(new DOMException('Cancelled', 'AbortError')); };
  if (signal.aborted) { reject(new DOMException('Cancelled', 'AbortError')); return; }
  signal.addEventListener('abort', abort, { once: true });
  xhr.send(body);
 });
}
