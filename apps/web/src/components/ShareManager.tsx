import { useEffect, useState } from 'react';
import { Copy, Link2, Trash2 } from 'lucide-react';
import { CreateShareDocument, FileSharesDocument, RevokeShareDocument, SharePermission, type FileSharesQuery } from '../generated/graphql';
import { explainError, mutate, query } from '../lib/api';
import { formatDate } from '../lib/format';
import { Button, Notice } from './ui';

export function ShareManager({ fileID }: { fileID: string }) {
 const [overview, setOverview] = useState<FileSharesQuery['fileShares']>();
 const [recipient, setRecipient] = useState('');
 const [expires, setExpires] = useState('86400');
 const [permission, setPermission] = useState(SharePermission.Download);
 const [link, setLink] = useState('');
 const [error, setError] = useState('');
 const [busy, setBusy] = useState(false);
 const [copied, setCopied] = useState(false);
 async function reload() { const result = await query(FileSharesDocument, { id: fileID }); setOverview(result.fileShares); }
 useEffect(() => { let alive = true; void query(FileSharesDocument, { id: fileID }).then(result => { if (alive) setOverview(result.fileShares); }, failure => { if (alive) setError(explainError(failure)); }); return () => { alive = false; }; }, [fileID]);
 async function create() {
  setBusy(true); setError(''); setCopied(false); setLink('');
  try {
   const result = await mutate(CreateShareDocument, { input: { fileId: fileID, permission, expiresInSeconds: Number(expires), recipientId: recipient.trim() || null } });
   setLink(new URL(result.createShare.url, window.location.origin).href);
   await reload();
  } catch (failure) { setError(explainError(failure)); } finally { setBusy(false); }
 }
 async function revoke(id: string) {
  setBusy(true); setError('');
  try { await mutate(RevokeShareDocument, { id }); setLink(''); await reload(); }
  catch (failure) { setError(explainError(failure)); } finally { setBusy(false); }
 }
 return <div className="form-stack">
  <div className="form-grid">
   <label>Expires after<select value={expires} onChange={event => setExpires(event.target.value)}><option value="3600">1 hour</option><option value="86400">1 day</option><option value="604800">7 days</option><option value="2592000">30 days</option></select></label>
   <label>Permission<select value={permission} onChange={event => setPermission(event.target.value as SharePermission)}><option value={SharePermission.Download}>Download only</option><option value={SharePermission.PreviewAndDownload}>Preview and download</option></select></label>
  </div>
  <label>Recipient user ID <span className="muted">(optional)</span><input value={recipient} onChange={event => setRecipient(event.target.value)} placeholder="Leave blank for anyone with the link" pattern="[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}" /></label>
  <p className="help-text">Without a recipient, anyone with this link can access the file until it expires or you revoke it. Sharing never grants editing rights.</p>
  <Button onClick={() => void create()} disabled={busy}><Link2 size={16} />{busy ? 'Working…' : 'Create sharing link'}</Button>
  {link && <div className="new-link"><label>Your new link<input value={link} readOnly onFocus={event => event.target.select()} /></label><Button variant="secondary" onClick={() => { void navigator.clipboard.writeText(link).then(() => setCopied(true), () => setError('Select the link above and copy it manually.')); }}><Copy size={15} />{copied ? 'Copied' : 'Copy link'}</Button><p className="help-text">Copy it now. The complete link will not be shown again.</p></div>}
  {error && <Notice error>{error}</Notice>}
  <div className="section-divider"><h3>Active links</h3><span className="muted">{overview?.downloadStarts ?? '—'} download starts</span></div>
  {!overview ? <p role="status" className="muted">Loading links…</p> : overview.shares.length === 0 ? <p className="muted">No active sharing links.</p> : <ul className="share-list">{overview.shares.map(item => <li key={item.id}><Link2 size={17} /><div><strong>{item.recipientId ? 'Restricted recipient' : 'Anyone with the link'}</strong><small>Expires {formatDate(item.expiresAt)} · {item.permission === SharePermission.Download ? 'Download' : 'Preview + download'}</small>{item.recipientId && <code>{item.recipientId}</code>}</div><Button variant="ghost" aria-label={`Revoke link ending ${item.id.slice(-6)}`} disabled={busy} onClick={() => void revoke(item.id)}><Trash2 size={16} /></Button></li>)}</ul>}
 </div>;
}
