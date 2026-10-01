import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import { useDropzone } from 'react-dropzone';
import { Download, Eye, FileText, FolderOpen, HardDrive, Link2, Plus, RefreshCw, Search, SlidersHorizontal, Trash2, UploadCloud, X } from 'lucide-react';
import { MoveFileDocument, SetFileTagsDocument, DeleteFileDocument, FileAccessDocument, FileAccessMode, UploadManyDocument, UploadOneDocument, VaultDocument, type FileFilter, type VaultQuery } from '../generated/graphql';
import { errorCode, explainError, mutate, preparePreview, query, startDownload } from '../lib/api';
import { formatBytes, formatDate, quotaPercent } from '../lib/format';
import { Button, Dialog, EmptyState, Notice, rememberFocus } from '../components/ui';
import { FilePreview, previewSupported } from '../components/FilePreview';
import type { UploadProgress } from '../lib/upload-transport';
import { TagSuggestions } from '../components/TagSuggestions';
import { FolderOrganizer, folderPaths, type Folder } from '../components/FolderOrganizer';
import { FileSharingSummary } from '../components/FileSharingSummary';
import { ShareManager } from '../components/ShareManager';

// Match the temporary demo's 10 MB transport budget; normal deployments retain 20 MB.
const uploadLimitMB = import.meta.env.VITE_TEMPORARY_DEMO === 'true' ? 10 : 20;
const uploadLimitBytes = uploadLimitMB * 1_000_000;

type VaultFile = VaultQuery['files']['nodes'][number];
export function VaultView() {
 const [data, setData] = useState<VaultQuery>();
 const [filter, setFilter] = useState<FileFilter>({});
 const [folderScope, setFolderScope] = useState('all');
 const [folders, setFolders] = useState<Folder[]>([]);
 const paths = useMemo(() => folderPaths(folders), [folders]);
 const effectiveFilter = useMemo(() => ({ ...filter, folderId: folderScope === 'all' || folderScope === 'root' ? null : folderScope, rootOnly: folderScope === 'root' }), [filter, folderScope]);
 const [moveTarget, setMoveTarget] = useState<VaultFile>();
 const [destination, setDestination] = useState('');
 const [moveBusy, setMoveBusy] = useState(false);
 const [moveError, setMoveError] = useState('');
 async function moveFile() {
  if (!moveTarget || moveBusy) return;
  setMoveBusy(true); setMoveError('');
  try { await mutate(MoveFileDocument, { fileId: moveTarget.id, folderId: destination || null }); setMoveTarget(undefined); setRefresh(value => value + 1); }
  catch (failure) { setMoveError(explainError(failure)); } finally { setMoveBusy(false); }
 }
 const [name, setName] = useState('');
 const [tagFilter, setTagFilter] = useState('');
 const [uploader, setUploader] = useState('');
 const [tagTarget, setTagTarget] = useState<VaultFile>();
 const [tagText, setTagText] = useState('');
 const [tagBusy, setTagBusy] = useState(false);
 const [tagError, setTagError] = useState('');
 const [mime, setMime] = useState('');
 const [minSize, setMinSize] = useState('');
 const [maxSize, setMaxSize] = useState('');
 const [from, setFrom] = useState('');
 const [before, setBefore] = useState('');
 const [advanced, setAdvanced] = useState(false);
 const [loading, setLoading] = useState(true);
 const [moreBusy, setMoreBusy] = useState(false);
 const [refresh, setRefresh] = useState(0);
 const [error, setError] = useState('');
 const [notice, setNotice] = useState('');
 const [uploadOpen, setUploadOpen] = useState(false);
 const [selected, setSelected] = useState<File[]>([]);
 const [uploadTags, setUploadTags] = useState('');
 const [uploading, setUploading] = useState(false);
 const [retryPending, setRetryPending] = useState(false);
 const [individualUploads, setIndividualUploads] = useState(false);
 type UploadItem = { key: string; status: 'queued' | 'uploading' | 'processing' | 'complete' | 'failed'; loaded: number; total?: number; error?: string };
 const uploadItems = useRef<UploadItem[]>([]);
 const [fileProgress, setFileProgress] = useState<UploadItem[]>([]);
 const [batchProgress, setBatchProgress] = useState<UploadProgress>();
 function updateUpload(index: number, update: Partial<UploadItem>) {
  uploadItems.current[index] = { ...uploadItems.current[index], ...update };
  setFileProgress([...uploadItems.current]);
 }
 const uploadKey = useRef('');
 function chooseFiles(files: File[]) {
  setSelected(files); uploadKey.current = crypto.randomUUID(); setRetryPending(false); setBatchProgress(undefined);
  uploadItems.current = files.map(() => ({ key: crypto.randomUUID(), status: 'queued', loaded: 0 })); setFileProgress([...uploadItems.current]);
 }
 const [deleting, setDeleting] = useState(false);
 const [deleteTarget, setDeleteTarget] = useState<VaultFile>();
 const [sharing, setSharing] = useState<VaultFile>();
 const [details,setDetails]=useState<VaultFile>();
 const [preview, setPreview] = useState<{ file: VaultFile; url?: string }>();
 const [actionID, setActionID] = useState('');
 const generation = useRef(0);
 const previewGeneration = useRef(0);

 useEffect(() => {
  const version = ++generation.current;
  let active = true; setLoading(true); setError('');
  void query(VaultDocument, { first: 20, filter: effectiveFilter }).then(result => {
   if (active && version === generation.current) setData(result);
  }, failure => { if (active) setError(explainError(failure)); }).finally(() => { if (active) setLoading(false); });
  return () => { active = false; };
 }, [effectiveFilter, refresh]);

 function applyFilters(event: FormEvent) {
  event.preventDefault();
  setFilter({
   tagsAll: tagFilter.trim() ? tagFilter.split(',').map(tag => tag.trim()) : null,
   uploaderNameContains: uploader.trim() || null,
   nameContains: name.trim() || null, mimeType: mime.trim() || null,
   minSizeBytes: minSize || null, maxSizeBytes: maxSize || null,
   createdFrom: from ? new Date(`${from}T00:00:00`).toISOString() : null,
   createdBefore: before ? new Date(`${before}T00:00:00`).toISOString() : null,
  });
 }
 async function loadMore() {
  if (!data?.files.pageInfo.endCursor) return;
  const version = generation.current; setMoreBusy(true);
  try {
   const result = await query(VaultDocument, { first: 20, filter: effectiveFilter, after: data.files.pageInfo.endCursor });
   if (version === generation.current) setData(previous => ({ ...result, files: { ...result.files, nodes: [...(previous?.files.nodes ?? []), ...result.files.nodes] } }));
  } catch (failure) { setError(explainError(failure)); } finally { setMoreBusy(false); }
 }
 const dropzone = useDropzone({
  noClick: true, noKeyboard: true, maxFiles: 10, maxSize: uploadLimitBytes, disabled: uploading || retryPending,
  onDrop: (accepted, rejected) => {
   setError(''); setNotice('');
   if (rejected.length) { setSelected([]); setError(`Choose up to 10 files, each no larger than ${uploadLimitMB} MB.`); return; }
   const bytes = accepted.reduce((sum, file) => sum + BigInt(file.size), 0n);
   if (bytes > BigInt(uploadLimitBytes)) { setSelected([]); setError(`The selected files exceed the ${uploadLimitMB} MB batch limit.`); return; }
   if (data && bytes > BigInt(data.quota.remainingBytes)) { setSelected([]); setError('These files exceed your remaining storage quota.'); return; }
   chooseFiles(accepted);
  },
 });
 async function upload() {
  if (!selected.length || uploading) return;
  setUploading(true); setError(''); setNotice('');
  const tags = uploadTags.trim() ? uploadTags.split(',').map(tag => tag.trim()) : [];
  if (individualUploads) {
   for (const [index, file] of selected.entries()) {
    if (uploadItems.current[index].status === 'complete') continue;
    updateUpload(index, { status: 'uploading', loaded: 0, total: undefined, error: undefined });
    try {
     await mutate(UploadOneDocument, { file, idempotencyKey: uploadItems.current[index].key, tags }, {
      uploads: [file], onUploadProgress: (progress: UploadProgress) => updateUpload(index, { ...progress, status: progress.total && progress.loaded >= progress.total ? 'processing' : 'uploading' }),
     });
     updateUpload(index, { status: 'complete' });
    } catch (failure) { updateUpload(index, { status: 'failed', error: explainError(failure) }); }
   }
   const saved = uploadItems.current.filter(item => item.status === 'complete').length;
   setRetryPending(saved !== selected.length);
   setNotice(saved === selected.length ? `Uploaded ${saved} ${saved === 1 ? 'file' : 'files'}.` : `${saved} of ${selected.length} files saved. Retry retries only unfinished files with their original keys.`);
   setRefresh(value => value + 1); setUploading(false);
   return;
  }
  setBatchProgress(undefined);
  try {
   if (selected.length === 1) await mutate(UploadOneDocument, { file: selected[0], idempotencyKey: uploadKey.current, tags }, { uploads: selected, onUploadProgress: setBatchProgress });
   else await mutate(UploadManyDocument, { files: selected, idempotencyKey: uploadKey.current, tags }, { uploads: selected, uploadMany: true, onUploadProgress: setBatchProgress });
   setNotice(`Uploaded ${selected.length} ${selected.length === 1 ? 'file' : 'files'}.`);
   chooseFiles([]); setUploadOpen(false); setRefresh(value => value + 1);
  } catch (failure) {
   const code = errorCode(failure);
   const uncertain = !code || code === 'INTERNAL_ERROR';
   setRetryPending(previous => previous || uncertain);
   setError(uncertain ? 'The upload result is uncertain. Retry this upload safely using the same selected files.' : explainError(failure));
  } finally { setUploading(false); }
 }
 async function saveTags() {
  if (!tagTarget) return;
  setTagBusy(true); setTagError('');
  try {
   await mutate(SetFileTagsDocument, { id: tagTarget.id, tags: tagText.trim() ? tagText.split(',').map(tag => tag.trim()) : [] });
   setTagTarget(undefined); setNotice('Private tags saved.'); setRefresh(value => value + 1);
  } catch (failure) { setTagError(explainError(failure)); } finally { setTagBusy(false); }
 }
 async function remove() {
  if (!deleteTarget) return;
  setDeleting(true); setError('');
  try { await mutate(DeleteFileDocument, { id: deleteTarget.id }); setNotice('File deleted. Its storage quota has been released.'); setDeleteTarget(undefined); setRefresh(value => value + 1); }
  catch (failure) { setError(explainError(failure)); } finally { setDeleting(false); }
 }
 async function access(file: VaultFile, mode: FileAccessMode) {
  setActionID(file.id); setError('');
  const request = ++previewGeneration.current;
  if (mode === FileAccessMode.Preview) setPreview({ file });
  try {
   const grant = await mutate(FileAccessDocument, { id: file.id, mode });
   if (mode === FileAccessMode.Download) await startDownload(grant.createFileAccess.url, file.name);
   else {
    const url = await preparePreview(grant.createFileAccess.url);
    if (request === previewGeneration.current) setPreview({ file, url });
   }
  } catch (failure) { setError(explainError(failure)); setPreview(undefined); }
  finally { setActionID(''); }
 }
 const percentage = data ? quotaPercent(data.quota.usedBytes, data.quota.quotaBytes) : 0;
 const filtered = Object.values(filter).some(Boolean);
 return <>
  <div className="page-heading"><div><span className="eyebrow">YOUR WORKSPACE, ORGANIZED</span><h1>My files</h1><p className="muted">A private home for everything you need to keep.</p></div><Button onClick={event => { rememberFocus(event); setUploadOpen(true); }}><Plus size={18} />Upload files</Button></div>
  <div className="stats-grid">
   <section className="stat-card"><span className="stat-icon"><FolderOpen size={20} /></span><span className="stat-label">Files in your vault</span><strong>{data?.storageStats.fileCount ?? '—'}</strong><small>Owned by you</small></section>
   <section className="stat-card"><span className="stat-icon"><HardDrive size={20} /></span><span className="stat-label">Storage used</span><strong>{data ? formatBytes(data.quota.usedBytes) : '—'}<span> / {data ? formatBytes(data.quota.quotaBytes) : '—'}</span></strong><div className="meter" role="progressbar" aria-label="Storage quota used" aria-valuemin={0} aria-valuemax={100} aria-valuenow={percentage}><span style={{ width: `${percentage}%` }} /></div><small>{data ? formatBytes(data.quota.remainingBytes) : '—'} available</small></section>
   <section className="stat-card"><span className="stat-icon"><FileText size={20} /></span><span className="stat-label">Duplicate content saved</span><strong>{data ? formatBytes(data.storageStats.savedBytes) : '—'}</strong><small>{data?.storageStats.savingsPercent ?? '0.00'}% less unique content · quota counts every file</small></section>
  </div>
  {error && <Notice error>{error}</Notice>}
  {notice && <Notice>{notice}</Notice>}
  {uploading && <Notice>Uploading your files. You can keep this window open while they finish.</Notice>}
  <FolderOrganizer scope={folderScope} onSelect={setFolderScope} onLoaded={setFolders} />
  <section className="panel files-panel" aria-label="Your files">
   <form className="file-toolbar" onSubmit={applyFilters}>
    <label className="search-box"><Search size={17} /><span className="sr-only">Search filenames</span><input value={name} onChange={event => setName(event.target.value)} placeholder="Search your files…" /><button type="submit" aria-label="Search"><Search size={16} /></button></label>
    <div className="toolbar-actions"><Button type="button" variant="secondary" aria-expanded={advanced} onClick={() => setAdvanced(!advanced)}><SlidersHorizontal size={16} />Filters</Button><Button type="button" variant="ghost" aria-label="Refresh files" disabled={loading} onClick={() => setRefresh(value => value + 1)}><RefreshCw size={17} /></Button></div>
    {advanced && <div className="filters-grid">
     <label>Tags (match all)<input value={tagFilter} onChange={event => setTagFilter(event.target.value)} placeholder="work, invoices" maxLength={700} /></label>
     <label>Uploader username<input value={uploader} onChange={event => setUploader(event.target.value)} maxLength={64} placeholder="Search within your own files" /></label>
     <label>File type<select value={mime} onChange={event => setMime(event.target.value)}><option value="">All types</option><option value="application/pdf">PDF</option><option value="image/png">PNG image</option><option value="image/jpeg">JPEG image</option><option value="image/webp">WebP image</option><option value="text/plain">Plain text</option><option value="application/zip">ZIP archive</option></select></label>
     <label>Minimum bytes<input inputMode="numeric" pattern="[0-9]*" value={minSize} onChange={event => setMinSize(event.target.value)} /></label><label>Maximum bytes<input inputMode="numeric" pattern="[0-9]*" value={maxSize} onChange={event => setMaxSize(event.target.value)} /></label>
     <label>Created on or after<input type="date" value={from} onChange={event => setFrom(event.target.value)} /></label><label>Created before<input type="date" value={before} onChange={event => setBefore(event.target.value)} /></label>
     <div className="filter-buttons"><Button type="submit" variant="secondary">Apply filters</Button><Button type="button" variant="ghost" onClick={() => { setName(''); setTagFilter(''); setUploader(''); setMime(''); setMinSize(''); setMaxSize(''); setFrom(''); setBefore(''); setFilter({}); }}>Clear</Button></div>
    </div>}
   </form>
   {loading ? <div className="table-loading" role="status">Loading your files…</div> : !data?.files.nodes.length ? <EmptyState title={filtered ? 'No matching files' : 'Your vault is ready'}>{filtered ? 'Try a different search or clear your filters.' : 'Upload your first file to get started. It stays private until you share it.'}</EmptyState> :
    <div className="table-scroll"><table><thead><tr><th scope="col">Name</th><th scope="col">Size</th><th scope="col">Added</th><th scope="col"><span className="sr-only">Actions</span></th></tr></thead><tbody>{data.files.nodes.map(file => <tr key={file.id}>
     <td><div className="file-name"><span className="file-icon"><FileText size={20} /></span><div><strong>{file.name}</strong><small>{file.detectedMIME.split(';')[0]}</small>{file.tags.length > 0 && <small aria-label={`Private tags for ${file.name}`}>{file.tags.join(', ')}</small>}</div></div></td><td className="nowrap">{formatBytes(file.sizeBytes)}</td><td className="muted nowrap">{formatDate(file.createdAt)}</td>
     <td><div className="row-actions"><Button variant="ghost" disabled={Boolean(actionID)} aria-label={`Download ${file.name}`} onClick={() => void access(file, FileAccessMode.Download)}><Download size={17} /></Button>
      {previewSupported(file.detectedMIME, file.name) && <Button variant="ghost" disabled={Boolean(actionID)} aria-label={`Preview ${file.name}`} onClick={event => { rememberFocus(event); void access(file, FileAccessMode.Preview); }}><Eye size={17} /></Button>}
      <Button variant="ghost" aria-label={`Details for ${file.name}`} onClick={event=>{rememberFocus(event);setDetails(file);}}>Details</Button>
      <Button variant="ghost" aria-label={`Move ${file.name}`} onClick={event => { rememberFocus(event); setMoveTarget(file); setDestination(file.folderId ?? ''); setMoveError(''); }}>Move</Button>
      <Button variant="ghost" aria-label={`Edit tags for ${file.name}`} onClick={event => { rememberFocus(event); setTagTarget(file); setTagText(file.tags.join(', ')); setTagError(''); }}>Tags</Button>
      <Button variant="ghost" aria-label={`Share ${file.name}`} onClick={event => { rememberFocus(event); setSharing(file); }}><Link2 size={17} /></Button><Button variant="ghost" aria-label={`Delete ${file.name}`} disabled={deleting} onClick={event => { rememberFocus(event); setDeleteTarget(file); }}><Trash2 size={17} /></Button></div></td>
    </tr>)}</tbody></table></div>}
   <div className="table-footer"><span>{data?.files.nodes.length ?? 0} {filtered ? 'matching files shown' : 'files shown'}</span>{data?.files.pageInfo.hasNextPage && <Button variant="secondary" disabled={moreBusy || loading} onClick={() => void loadMore()}>{moreBusy ? 'Loading…' : 'Load more'}</Button>}</div>
  </section>
  <Dialog open={uploadOpen} onClose={() => { if (!uploading) setUploadOpen(false); }} title="Upload files" description={`Choose up to 10 files. Each batch can contain up to ${uploadLimitMB} MB, within your remaining quota.`}>
   <label>Upload mode<select value={individualUploads ? 'individual' : 'atomic'} disabled={uploading || retryPending || fileProgress.some(item => item.status !== 'queued')} onChange={event => setIndividualUploads(event.target.value === 'individual')}><option value="atomic">All-or-nothing batch</option><option value="individual">Individual files with separate progress</option></select></label>
   <p className="help-text">{individualUploads ? 'Each file saves independently. Other files may succeed if one fails.' : 'All files publish together. Progress describes the entire request.'}</p>
   <label>Upload tags separated by commas<input value={uploadTags} onChange={event => setUploadTags(event.target.value)} maxLength={700} disabled={uploading || retryPending || fileProgress.some(item => item.status !== 'queued')} /></label>
   <TagSuggestions value={uploadTags} onChange={setUploadTags} disabled={uploading || retryPending || fileProgress.some(item => item.status !== 'queued')} />
   <p className="help-text">These owner-private tags apply to each selected logical file. You can also type custom tags.</p>
   <div {...dropzone.getRootProps({ className: `dropzone ${dropzone.isDragActive ? 'drag-active' : ''}` })}>
    <input {...dropzone.getInputProps({ 'aria-label': 'Select files to upload' })} /><UploadCloud size={36} /><h3>Drop your files here</h3><p className="muted">or choose them from your device</p><Button variant="secondary" onClick={dropzone.open} disabled={uploading || retryPending}>Choose files</Button>
   </div>
   {selected.length > 0 && <ul className="upload-list">{selected.map((file, index) => <li key={`${file.name}-${index}`}><FileText size={17} /><span>{file.name}<small>{formatBytes(file.size)}</small>{individualUploads && fileProgress[index] && <UploadStatus item={fileProgress[index]} name={file.name} />}</span><Button variant="ghost" aria-label={`Remove ${file.name} from selection`} disabled={uploading || retryPending} onClick={() => chooseFiles(selected.filter((_, i) => i !== index))}><X size={15} /></Button></li>)}</ul>}
   {!individualUploads && uploading && batchProgress && <UploadStatus item={{ ...batchProgress, status: batchProgress.total && batchProgress.loaded >= batchProgress.total ? 'processing' : 'uploading' }} name="Batch" />}
   {notice && individualUploads && <Notice>{notice}</Notice>}
   {error && <Notice error>{error}</Notice>}
   <div className="dialog-actions"><Button variant="secondary" disabled={uploading || !selected.length} onClick={() => chooseFiles([])}>Clear selection (keeps saved files)</Button><span className="muted">{selected.length} selected · {formatBytes(selected.reduce((sum, file) => sum + file.size, 0))}</span><Button disabled={!selected.length || uploading} onClick={() => void upload()}>{uploading ? 'Uploading…' : retryPending ? 'Retry upload safely' : 'Upload selected files'}</Button></div>
  </Dialog>
  <Dialog open={Boolean(deleteTarget)} onClose={() => setDeleteTarget(undefined)} title="Delete this file?" description="This removes your file and revokes its sharing links. Other users' independently owned copies are unaffected.">
   <p className="delete-filename">{deleteTarget?.name}</p><div className="dialog-actions"><Button variant="secondary" onClick={() => setDeleteTarget(undefined)}>Keep file</Button><Button variant="danger" disabled={deleting} onClick={() => void remove()}>{deleting ? 'Deleting…' : 'Delete file'}</Button></div>
  </Dialog>
  <Dialog open={Boolean(tagTarget)} onClose={() => { if (!tagBusy) setTagTarget(undefined); }} title="Private file tags" description="Visible only to you, including when this file is shared. Up to 20 tags, each 1–32 characters: letters, numbers, spaces, hyphens or underscores.">
   <form onSubmit={event => { event.preventDefault(); void saveTags(); }}>
    <label>Tags separated by commas<input value={tagText} onChange={event => setTagText(event.target.value)} maxLength={700} disabled={tagBusy} /></label>
    <TagSuggestions value={tagText} onChange={setTagText} disabled={tagBusy} />
    <p className="muted">Tags are saved in lowercase. Leave blank to remove all tags.</p>
    {tagError && <Notice error>{tagError}</Notice>}
    <div className="dialog-actions"><Button type="submit" disabled={tagBusy}>{tagBusy ? 'Saving…' : 'Save tags'}</Button></div>
   </form>
  </Dialog>
  <Dialog open={Boolean(moveTarget)} onClose={() => { if (!moveBusy) setMoveTarget(undefined); }} title="Move file" description={moveTarget?.name}>
   <form className="form-stack" onSubmit={event => { event.preventDefault(); void moveFile(); }}>
    <label>Destination folder<select aria-label="Destination folder" value={destination} disabled={moveBusy} onChange={event => setDestination(event.target.value)}><option value="">Root</option>{folders.map(folder => <option key={folder.id} value={folder.id}>{paths.get(folder.id)}</option>)}</select></label>
    {moveError && <Notice error>{moveError}</Notice>}
    <Button type="submit" disabled={moveBusy}>{moveBusy ? 'Moving…' : 'Move file'}</Button>
   </form>
  </Dialog>
  <Dialog open={Boolean(details)} onClose={()=>setDetails(undefined)} title="File details" description={details?.name}>
   {details && <div className="form-stack"><dl>
    <dt>Owner</dt><dd>You</dd>
    <dt>Folder</dt><dd>{details.folderId ? paths.get(details.folderId) ?? 'Folder unavailable' : 'Root'}</dd>
    <dt>Logical file ID</dt><dd>{details.id}</dd>
    <dt>Logical size</dt><dd>{formatBytes(details.sizeBytes)} ({details.sizeBytes} bytes)</dd>
    <dt>Detected MIME type</dt><dd>{details.detectedMIME}</dd>
    <dt>Uploaded</dt><dd>{formatDate(details.createdAt)}</dd>
    <dt>Private tags</dt><dd>{details.tags.join(', ') || 'None'}</dd>
    <dt>Preview</dt><dd>{previewSupported(details.detectedMIME, details.name) ? 'Preview available' : 'Download only'}</dd>
   </dl><FileSharingSummary key={details.id} fileID={details.id} /><p className="help-text">Private to your account unless explicitly shared. Duplicate contents still use this file's full logical quota. Storage savings are shown for your account, not inferred from other users' files. Embedded metadata is not extracted or displayed; original downloads may retain it. No malware scan is claimed.</p></div>}
  </Dialog>
  <Dialog open={Boolean(sharing)} onClose={() => setSharing(undefined)} title="Share file" description={sharing?.name}>{sharing && <ShareManager key={sharing.id} fileID={sharing.id} canPreview={previewSupported(sharing.detectedMIME, sharing.name)} />}</Dialog>
  <Dialog open={Boolean(preview)} onClose={() => { previewGeneration.current++; setPreview(undefined); }} title="File preview" description={preview?.file.name} wide>
   {preview?.url ? <FilePreview url={preview.url} media={preview.file.detectedMIME} name={preview.file.name} /> : <p role="status">Preparing a secure preview…</p>}
  </Dialog>
 </>;
}

function UploadStatus({ item, name }: { item: { status: string; loaded: number; total?: number; error?: string }; name: string }) {
 const percent = item.total ? Math.min(100, Math.floor(item.loaded * 100 / item.total)) : undefined;
 if (item.status === 'queued') return <small>Queued</small>;
 if (item.status === 'complete') return <small role="status">Saved</small>;
 if (item.status === 'failed') return <small role="alert">{item.error ?? 'Upload failed. Retry is available.'}</small>;
 return <div><progress aria-label={name + ' upload progress'} max={100} value={percent} /><small>{percent === undefined ? '' : percent + '% · '}{formatBytes(item.loaded)} request bytes transferred{item.total ? ' of ' + formatBytes(item.total) : ''}</small><small role="status">{item.status === 'processing' ? 'Request sent. Waiting for the server to save the file.' : 'Sending request…'}</small></div>;
}
