# Authorized previews

Details contains metadata only. Share manages links; Preview renders content.
The same backend ownership/session/capability checks authorize private and
shared content. Preview does not grant access to arbitrary files or storage URLs.

Supported detected MIME types are PNG, JPEG, WebP, plain text (TXT filenames
only), and PDF. Filename extensions alone never authorize a format. HTML, SVG,
scripts and executables remain download-only. DOC/DOCX remain download-only:
conversion would require a separately isolated document-processing service.

TXT renders through React text nodes, never HTML, with a 256 KiB display bound.
PDF uses pinned PDF.js 6.3.289 with a same-origin worker and canvas rendering.
There is no scripting manager, form layer, annotation/link layer, attachment
handler, document HTML injection, or native PDF embed. XFA, dynamic font loading,
worker resource fetching and WebAssembly are disabled. Preview limits are
10 MB input, 200 pages, 4 million image pixels and one bounded page canvas at a
time. Fetch/document loading and page rendering have 20-second timeouts. The
viewer provides page navigation and releases workers on close. This reduces
attack surface; it does not claim that every malformed document is harmless or
that the browser can prevent screenshots or copying.

The original byte endpoint retains nosniff, private/no-store, same-origin
resource policy and sandbox CSP. PDF preview responses use application/pdf and
inline disposition; download responses remain attachments. Worker and viewer
assets are served from the application, without a third-party CDN or relaxed
script policy. Keep the dependency updated and run security/advisory checks.

PDF preview is visual only. Owners and download-enabled recipients can retrieve
the original for accessible document readers. View-only recipients must request
download permission from the owner. Text extraction, PDF title/author, image EXIF/GPS,
malware scanning and document conversion are not fabricated.

References:
- [PDF.js document and rendering options](https://mozilla.github.io/pdf.js/api/draft/module-pdfjsLib.html)
- [Historical PDF.js JavaScript execution advisory](https://github.com/mozilla/pdf.js/security/advisories/GHSA-wgrm-67xf-hhpq)
