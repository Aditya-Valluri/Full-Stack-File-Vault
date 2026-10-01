import { lazy, Suspense, useEffect, useState } from 'react';
import { Notice } from './ui';
import { fetchPreview } from '../lib/api';

const PDFPreview = lazy(() => import('./PDFPreview'));
export function previewSupported(media: string, name: string): boolean {
 const kind = media.split(';')[0];
 return ['image/png', 'image/jpeg', 'image/webp', 'text/plain', 'application/pdf'].includes(kind) && (kind !== 'text/plain' || name.toLowerCase().endsWith('.txt'));
}

// Authorization still happens on the original short-lived content URL.
// Text is rendered as a React text node, never interpreted as HTML.
function TextPreview({ url, downloadAllowed }: { url: string; downloadAllowed: boolean }) {
 const [text, setText] = useState<string>();
 const [truncated, setTruncated] = useState(false);
 const [error, setError] = useState(false);
 useEffect(() => {
  const controller = new AbortController();
  const timer = window.setTimeout(() => controller.abort(), 15000);
  let active = true;
  void (async () => {
   const response = await fetchPreview(url, controller.signal);
   if (!response.ok || response.headers.get('content-type')?.split(';')[0] !== 'text/plain' || !response.body) throw new Error();
   const reader = response.body.getReader();
   const decoder = new TextDecoder('utf-8');
   let remaining = 256 * 1024, result = '', clipped = false;
   try {
    while (true) {
     const { done, value } = await reader.read();
     if (done) break;
     const take = Math.min(remaining, value.byteLength);
     result += decoder.decode(value.subarray(0, take), { stream: true });
     remaining -= take;
     if (!remaining) { clipped = true; break; }
    }
    result += decoder.decode();
   } finally { await reader.cancel(); }
   if (active) { setText(result); setTruncated(clipped); }
  })().catch(() => { if (active) setError(true); }).finally(() => window.clearTimeout(timer));
  return () => { active = false; controller.abort(); window.clearTimeout(timer); };
 }, [url]);
 if (error) return <Notice error>Preview unavailable. Close this panel and try again.</Notice>;
 if (text === undefined) return <p role="status">Loading text preview…</p>;
 return <>{truncated && <p className="help-text">Showing the first 256 KiB. {downloadAllowed ? 'Download the file to read the rest.' : 'Ask the owner for download access to read the rest.'}</p>}<pre className="text-preview">{text}</pre></>;
}
function ImagePreview({ url, name }: { url: string; name: string }) {
 const [failed, setFailed] = useState(false);
 return failed ? <Notice error>Preview unavailable. Close this panel and try again.</Notice> : <img className="preview-image" src={url} alt={name} onError={() => setFailed(true)} />;
}
export function FilePreview({ url, media, name, downloadAllowed = true }: { url: string; media: string; name: string; downloadAllowed?: boolean }) {
 const kind = media.split(';')[0];
 if (kind === 'application/pdf') return <Suspense fallback={<p role="status">Loading PDF viewer…</p>}><PDFPreview key={url} url={url} name={name} downloadAllowed={downloadAllowed} /></Suspense>;
 if (kind === 'text/plain') return <TextPreview key={url} url={url} downloadAllowed={downloadAllowed} />;
 if (['image/png', 'image/jpeg', 'image/webp'].includes(kind)) return <ImagePreview key={url} url={url} name={name} />;
 return <Notice error>This file type is download-only.</Notice>;
}
