import { useEffect, useRef, useState } from 'react';
import { AnnotationMode, getDocument, GlobalWorkerOptions, type PDFDocumentProxy } from 'pdfjs-dist';
import workerURL from 'pdfjs-dist/build/pdf.worker.min.mjs?url';
import { Button, Notice } from './ui';
import { fetchPreview } from '../lib/api';

GlobalWorkerOptions.workerSrc = workerURL;
const MAX_BYTES = 10_000_000;
const MAX_PIXELS = 4_000_000;

// Canvas-only rendering: no viewer scripting manager, forms, attachments,
// annotation DOM, external links, document HTML, or metadata injection.
export default function PDFPreview({ url, name, downloadAllowed }: { url: string; name: string; downloadAllowed: boolean }) {
 const canvas = useRef<HTMLCanvasElement>(null);
 const [document, setDocument] = useState<PDFDocumentProxy>();
 const [page, setPage] = useState(1);
 const [ready, setReady] = useState(false);
 const [error, setError] = useState('');
 useEffect(() => {
  let active = true;
  let task: ReturnType<typeof getDocument> | undefined;
  const controller = new AbortController();
  const timer = window.setTimeout(() => { controller.abort(); void task?.destroy(); if (active) setError('PDF preview timed out.'); }, 20000);
  void (async () => {
   const response = await fetchPreview(url, controller.signal);
   if (!response.ok || response.headers.get('content-type')?.split(';')[0] !== 'application/pdf' || !response.body) throw new Error();
   const length = Number(response.headers.get('content-length'));
   if (length > MAX_BYTES) throw new Error();
   const reader = response.body.getReader();
   const chunks: Uint8Array[] = [];
   let size = 0;
   try {
    while (true) {
     const { value, done } = await reader.read();
     if (done) break;
     size += value.byteLength;
     if (size > MAX_BYTES) throw new Error();
     chunks.push(value);
    }
   } finally { await reader.cancel(); }
   if (!active) return;
   const bytes = new Uint8Array(size);
   let offset = 0;
   for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
   task = getDocument({ data: bytes, enableXfa: false, disableFontFace: true,
    useSystemFonts: false, useWorkerFetch: false, useWasm: false,
    maxImageSize: MAX_PIXELS, canvasMaxAreaInBytes: MAX_PIXELS * 4,
    stopAtErrors: true, verbosity: 0 });
   task.onPassword = () => { void task?.destroy(); if (active) setError('Password-protected PDFs are download-only.'); };
   const pdf = await task.promise;
   if (pdf.numPages > 200) throw new Error();
   if (active) setDocument(pdf);
  })().catch(() => { if (active) setError('PDF preview unavailable. The file may be unsupported, invalid, or too large.'); void task?.destroy(); })
   .finally(() => window.clearTimeout(timer));
  return () => { active = false; controller.abort(); window.clearTimeout(timer); void task?.destroy(); };
 }, [url]);
 useEffect(() => {
  if (!document || !canvas.current) return;
  let active = true;
  let rendering: ReturnType<Awaited<ReturnType<PDFDocumentProxy['getPage']>>['render']> | undefined;
  setReady(false);
  const timer = window.setTimeout(() => { rendering?.cancel(); if (active) setError('This page took too long to render.'); }, 20000);
  void (async () => {
   const pdfPage = await document.getPage(page);
   if (!active || !canvas.current) return;
   const original = pdfPage.getViewport({ scale: 1 });
   if (!Number.isFinite(original.width * original.height) || original.width <= 0 || original.height <= 0) throw new Error();
   const scale = Math.min(1.5, 1600 / original.width, 2400 / original.height, Math.sqrt(MAX_PIXELS / (original.width * original.height)));
   const viewport = pdfPage.getViewport({ scale });
   canvas.current.width = Math.ceil(viewport.width);
   canvas.current.height = Math.ceil(viewport.height);
   rendering = pdfPage.render({ canvas: canvas.current, viewport, annotationMode: AnnotationMode.DISABLE });
   await rendering.promise;
   if (active) setReady(true);
   pdfPage.cleanup();
  })().catch(() => { if (active) setError('This PDF page could not be rendered.'); })
   .finally(() => window.clearTimeout(timer));
  return () => { active = false; rendering?.cancel(); window.clearTimeout(timer); };
 }, [document, page]);
 if (error) return <Notice error>{error}</Notice>;
 return <div className="pdf-preview">
  {document && <div className="dialog-actions"><Button variant="secondary" disabled={!ready || page <= 1} onClick={() => setPage(value => value - 1)}>Previous page</Button><span>Page {page} of {document.numPages}</span><Button variant="secondary" disabled={!ready || page >= document.numPages} onClick={() => setPage(value => value + 1)}>Next page</Button></div>}
  {!ready && <p role="status">Rendering PDF preview…</p>}
  <canvas ref={canvas} role="img" aria-label={name + ', page ' + page} className="pdf-canvas" hidden={!ready} />
  <p className="help-text">Visual preview only. Interactive PDF content is disabled. {downloadAllowed ? 'Download for the original document and accessible text.' : 'Ask the owner for download access if you need the original document or accessible text.'}</p>
 </div>;
}
