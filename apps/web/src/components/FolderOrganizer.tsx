import { useEffect, useMemo, useState } from 'react';
import { CreateFolderDocument, DeleteFolderDocument, FoldersDocument, RenameFolderDocument, type FoldersQuery } from '../generated/graphql';
import { explainError, mutate, query } from '../lib/api';
import { Button, Dialog, Notice } from './ui';

export type Folder = FoldersQuery['folders'][number];
// Bounded walks and a hash index avoid repeated whole-tree scans.
export function folderPaths(folders: Folder[]): Map<string, string> {
 const index = new Map(folders.map(folder => [folder.id, folder]));
 return new Map(folders.map(folder => {
  const parts: string[] = []; const seen = new Set<string>(); let current: Folder | undefined = folder;
  while (current && !seen.has(current.id) && parts.length < 20) {
   seen.add(current.id); parts.unshift(current.name); current = current.parentId ? index.get(current.parentId) : undefined;
  }
  return [folder.id, '/' + parts.join('/')];
 }));
}
export function FolderOrganizer({ scope, onSelect, onLoaded }: { scope: string; onSelect: (id: string) => void; onLoaded: (folders: Folder[]) => void }) {
 const [folders, setFolders] = useState<Folder[]>([]);
 const [error, setError] = useState('');
 const [busy, setBusy] = useState(false);
 const [ready, setReady] = useState(false);
 const [editing, setEditing] = useState<'create' | 'rename'>();
 const [name, setName] = useState('');
 const paths = useMemo(() => folderPaths(folders), [folders]);
 const selected = folders.find(folder => folder.id === scope);
 async function reload() {
  const result = await query(FoldersDocument, {});
  setFolders(result.folders); onLoaded(result.folders); setReady(true);
 }
 useEffect(() => {
  let active = true;
  void query(FoldersDocument, {}).then(result => { if (active) { setFolders(result.folders); onLoaded(result.folders); setReady(true); } }, failure => { if (active) setError(explainError(failure)); });
  return () => { active = false; };
 }, [onLoaded]);
 async function save() {
  if (busy) return;
  setBusy(true); setError('');
  try {
   if (editing === 'rename' && selected) await mutate(RenameFolderDocument, { id: selected.id, name });
   else await mutate(CreateFolderDocument, { name, parentId: selected?.id ?? null });
   await reload(); setEditing(undefined); setName('');
  } catch (failure) { setError(explainError(failure)); } finally { setBusy(false); }
 }
 async function remove() {
  if (!selected || busy) return;
  setBusy(true); setError('');
  try { await mutate(DeleteFolderDocument, { id: selected.id }); onSelect(selected.parentId ?? 'root'); await reload(); }
  catch { setError('Could not delete this folder. Move its files and remove child folders first, then try again.'); }
  finally { setBusy(false); }
 }
 const children = folders.filter(folder => folder.parentId === (selected?.id ?? null));
 return <section className="panel folder-panel" aria-label="Folder organization">
  <div className="form-grid">
   <label>Browse folders<select aria-label="Browse folders" value={scope} onChange={event => onSelect(event.target.value)} disabled={busy || !ready}>
    <option value="all">All folders — search everywhere</option><option value="root">Root files</option>
    {folders.map(folder => <option key={folder.id} value={folder.id}>{paths.get(folder.id)}</option>)}
   </select></label>
   <div className="folder-actions">
    <Button variant="secondary" disabled={busy || !ready} onClick={() => { setEditing('create'); setName(''); setError(''); }}>New folder</Button>
    {selected && <><Button variant="secondary" disabled={busy} onClick={() => { setEditing('rename'); setName(selected.name); setError(''); }}>Rename folder</Button><Button variant="ghost" disabled={busy} onClick={() => void remove()}>Delete empty folder</Button></>}
   </div>
  </div>
  {selected && <Button variant="ghost" disabled={busy} onClick={() => onSelect(selected.parentId ?? 'root')}>Parent folder</Button>}
  {children.length > 0 && <nav aria-label="Child folders" className="folder-actions">{children.map(folder => <Button key={folder.id} variant="ghost" onClick={() => onSelect(folder.id)}>{folder.name}</Button>)}</nav>}
  <p className="help-text">Uploads start at root. Use Move on a file to organize it. Folder selection combines with your search and tags.</p>
  {!editing && error && <Notice error>{error}</Notice>}
  <Dialog open={Boolean(editing)} onClose={() => { if (!busy) setEditing(undefined); }} title={editing === 'rename' ? 'Rename folder' : 'Create folder'}>
   <form className="form-stack" onSubmit={event => { event.preventDefault(); void save(); }}>
    <p className="help-text">In {selected && editing === 'create' ? paths.get(selected.id) : 'your private folder tree'}.</p>
    <label>Folder name<input required maxLength={100} value={name} onChange={event => setName(event.target.value)} disabled={busy} /></label>
    {error && <Notice error>{error}</Notice>}
    <Button type="submit" disabled={busy || !name.trim()}>{busy ? 'Saving…' : 'Save folder'}</Button>
   </form>
  </Dialog>
 </section>;
}
