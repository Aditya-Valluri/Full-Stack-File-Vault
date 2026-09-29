import { useEffect, useState } from 'react';
import { Copy, Link2, Trash2 } from 'lucide-react';
import { CreateShareDocument, FileSharesDocument, RevokeShareDocument, SharePermission, type FileSharesQuery } from '../generated/graphql';
import { explainError, mutate, query } from '../lib/api';
import { formatDate } from '../lib/format';
import { Button, Notice } from './ui';

export function ShareManager({ fileID }: { fileID: string }) {
 const [overview, setOverview] = useState<FileSharesQuery['fileShares']>();
 const [recipient, setRecipient] = useState('');
 const [mode,setMode]=useState('public');
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
   const result = await mutate(CreateShareDocument, { input: { fileId: fileID, permission, expiresInSeconds: Number(expires), recipientId: mode === 'recipient' ? recipient.trim() : null } });
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
  <label>Share mode<select value={mode} onChange={event=>setMode(event.target.value)}><option value="public">Anyone with link</option><option value="recipient">Specific recipient</option></select></label>
  {mode === 'recipient' && <label>Recipient user ID<input value={recipient} onChange={event => setRecipient(event.target.value)} placeholder="Account ID supplied by the recipient" pattern="[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}" /></label>}
  <p className="help-text">Specific recipients must sign in to the selected account. Each created link has a separate token. Reuse of a link cannot prove who forwarded it.</p>
  <p className="help-text">Without a recipient, anyone with this link can access the file until it expires or you revoke it. Sharing never grants editing rights.</p>
  <Button onClick={() => void create()} disabled={busy || (mode === 'recipient' && !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(recipient.trim()))}><Link2 size={16} />{busy ? 'Working…' : 'Create sharing link'}</Button>
  {link && <div className="new-link"><label>Your new link<input value={link} readOnly onFocus={event => event.target.select()} /></label><Button variant="secondary" onClick={() => { void navigator.clipboard.writeText(link).then(() => setCopied(true), () => setError('Select the link above and copy it manually.')); }}><Copy size={15} />{copied ? 'Copied' : 'Copy link'}</Button><p className="help-text">Copy it now. The complete link will not be shown again.</p></div>}
  {error && <Notice error>{error}</Notice>}
  <div className="section-divider"><h3>Active links</h3><span className="muted">{overview?.downloadStarts ?? '—'} download starts</span></div>
  {!overview ? <p role="status" className="muted">Loading links…</p> : overview.shares.length === 0 ? <p className="muted">No active sharing links.</p> : <ul className="share-list">{overview.shares.map(item => <li key={item.id}><Link2 size={17} /><div><strong>{item.recipientId ? 'Restricted recipient' : 'Anyone with the link'}</strong><small>Expires {formatDate(item.expiresAt)} · {item.permission === SharePermission.Download ? 'Download' : 'Preview + download'}</small>{item.recipientId && <code>{item.recipientId}</code>}</div><Button variant="ghost" aria-label={`Revoke link ending ${item.id.slice(-6)}`} disabled={busy} onClick={() => void revoke(item.id)}><Trash2 size={16} /></Button></li>)}</ul>}
  <div className="section-divider"><h3>Share activity</h3><Button variant="ghost" disabled={busy} onClick={()=>void reload().catch(failure=>setError(explainError(failure)))}>Refresh activity</Button></div>
  <p className="help-text">Latest 100 events for this file. Opened means an authorized share-page request, not a unique person. Download started does not confirm transfer completion. No IP or device data is collected.</p>
  {overview?.activity.length ? <ul className="share-list">{overview.activity.map(event=><li key={event.id}><div><strong>{event.recipientId ? 'Authenticated recipient '+event.recipientId : 'Anonymous visitor'} — {event.kind === 'OPENED' ? 'Opened' : 'Download started'}</strong><small>{formatDate(event.occurredAt)} · Share {event.shareId.slice(-6)} · {event.status.toLowerCase()}</small></div></li>)}</ul> : <p className="muted">No recorded activity.</p>}
 </div>;
}
