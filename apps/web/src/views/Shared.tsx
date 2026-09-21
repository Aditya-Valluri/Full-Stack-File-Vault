import { useEffect, useState } from 'react';
import { Download, Eye, FileText, Link2, ShieldCheck } from 'lucide-react';
import { FileAccessMode, SharedAccessDocument, SharedFileDocument, type SharedFileQuery } from '../generated/graphql';
import { explainError, mutate, preparePreview, query, startDownload } from '../lib/api';
import { formatBytes, formatDate } from '../lib/format';
import { Button, Dialog, Notice, rememberFocus } from '../components/ui';

export function SharedView({ initialToken, signedIn, onSignIn }: { initialToken: string; signedIn: boolean; onSignIn: () => void }) {
 const [token, setToken] = useState(initialToken);
 const [linkInput, setLinkInput] = useState('');
 const [file, setFile] = useState<SharedFileQuery['sharedFile']>();
 const [error, setError] = useState('');
 const [loading, setLoading] = useState(Boolean(initialToken));
 const [busy, setBusy] = useState(false);
 const [preview, setPreview] = useState('');
 useEffect(() => {
  if (!token) return;
  let active = true; setLoading(true); setError('');
  void query(SharedFileDocument, { token }).then(result => { if (active) setFile(result.sharedFile); }, failure => { if (active) { setFile(null); setError(explainError(failure)); } }).finally(() => { if (active) setLoading(false); });
  return () => { active = false; };
 }, [token]);
 async function access(mode: FileAccessMode) {
  if (!file) return;
  setBusy(true); setError('');
  try {
   const result = await mutate(SharedAccessDocument, { token, mode });
   if (mode === FileAccessMode.Download) await startDownload(result.createSharedAccess.url, file.name);
   else setPreview(await preparePreview(result.createSharedAccess.url));
  } catch (failure) { setError(explainError(failure)); } finally { setBusy(false); }
 }
 return <div className="shared-page"><div className="page-heading"><div><span className="eyebrow">SHARED WITH YOU</span><h1>A file, shared securely</h1><p className="muted">Access lasts only as long as the owner allows.</p></div></div>
  {!token ? <section className="panel shared-card"><Link2 size={30} /><h2>Open a sharing link</h2><p className="muted">Paste the complete link you received. Links are not saved in this browser.</p><form className="form-stack" onSubmit={event => { event.preventDefault(); try { const url = new URL(linkInput, window.location.origin); if (url.origin !== window.location.origin || url.pathname !== '/share' || !/^[A-Za-z0-9_-]{43}$/.test(url.hash.slice(1))) throw new Error(); setToken(url.hash.slice(1)); setLinkInput(''); } catch { setError('Paste a valid Full Stack File Vault sharing link.'); } }}><label>Sharing link<input required value={linkInput} onChange={event => setLinkInput(event.target.value)} placeholder="Paste your link" /></label><Button type="submit">Open shared file</Button></form></section> :
   loading ? <div className="panel shared-card" role="status">Checking this sharing link…</div> :
   file ? <section className="panel shared-card"><span className="large-file-icon"><FileText size={38} /></span><h2>{file.name}</h2><p className="muted">{formatBytes(file.sizeBytes)} · {file.detectedMIME.split(';')[0]}</p><div className="shared-expiry"><ShieldCheck size={17} />Available until {formatDate(file.expiresAt)}</div><div className="shared-actions"><Button disabled={busy} onClick={() => void access(FileAccessMode.Download)}><Download size={17} />{busy ? 'Preparing…' : 'Download file'}</Button>{file.previewAllowed && <Button variant="secondary" disabled={busy} onClick={event => { rememberFocus(event); void access(FileAccessMode.Preview); }}><Eye size={17} />Preview</Button>}</div><p className="help-text">A sharing link allows reading this file. It does not give access to the owner's vault.</p></section> : <section className="panel shared-card"><h2>This link is unavailable</h2><p className="muted">It may have expired, been revoked, or be restricted to a different account.</p>{!signedIn && <Button onClick={onSignIn}>Sign in to check restricted access</Button>}</section>}
  {error && <Notice error>{error}</Notice>}
  <Dialog open={Boolean(preview)} onClose={() => setPreview('')} title="Shared file preview" description={file?.name} wide>{preview && <img className="preview-image" src={preview} alt={file?.name ?? 'Shared file'} onError={() => { setPreview(''); setError('This preview expired. Open it again to retry.'); }} />}</Dialog>
 </div>;
}
