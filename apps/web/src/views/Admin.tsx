import { useEffect, useRef, useState } from 'react';
import { Activity, Files, HardDrive, RefreshCw, Settings2, ShieldCheck, UserRound, Users } from 'lucide-react';
import { AdminAuditDocument, AdminFilesDocument, AdminOverviewDocument, AdminUsersDocument, SetDisabledDocument, SetQuotaDocument, UserRole, type AdminAuditQuery, type AdminFilesQuery, type AdminOverviewQuery, type AdminUsersQuery } from '../generated/graphql';
import { explainError, mutate, query } from '../lib/api';
import { formatBytes, formatDate } from '../lib/format';
import { Button, Dialog, EmptyState, Notice, rememberFocus } from '../components/ui';

type User = AdminUsersQuery['adminUsers']['nodes'][number];
export function AdminView() {
 const [tab, setTab] = useState<'users' | 'files' | 'audit'>('users');
 const [stats, setStats] = useState<AdminOverviewQuery['adminStorageStats']>();
 const [users, setUsers] = useState<AdminUsersQuery['adminUsers']>();
 const [files, setFiles] = useState<AdminFilesQuery['adminFiles']>();
 const [audit, setAudit] = useState<AdminAuditQuery['adminAudit']>();
 const [owner, setOwner] = useState('');
 const [ownerInput, setOwnerInput] = useState('');
 const [refresh, setRefresh] = useState(0);
 const [loading, setLoading] = useState(true);
 const [moreBusy, setMoreBusy] = useState(false);
 const [busy, setBusy] = useState(false);
 const [error, setError] = useState('');
 const [notice, setNotice] = useState('');
 const [quotaTarget, setQuotaTarget] = useState<User>();
 const [quota, setQuota] = useState('');
 const [statusTarget, setStatusTarget] = useState<User>();
 const generation = useRef(0);

 useEffect(() => {
  let active = true;
  void query(AdminOverviewDocument, {}).then(result => { if (active) setStats(result.adminStorageStats); }, failure => { if (active) setError(explainError(failure)); });
  return () => { active = false; };
 }, [refresh]);
 useEffect(() => {
  let active = true; generation.current++; setLoading(true); setError('');
  const request = tab === 'users' ? query(AdminUsersDocument, { first: 20 }).then(result => { if (active) setUsers(result.adminUsers); })
   : tab === 'files' ? query(AdminFilesDocument, { first: 20, owner: owner || null }).then(result => { if (active) setFiles(result.adminFiles); })
    : query(AdminAuditDocument, { first: 20 }).then(result => { if (active) setAudit(result.adminAudit); });
  void request.catch(failure => { if (active) setError(explainError(failure)); }).finally(() => { if (active) setLoading(false); });
  return () => { active = false; };
 }, [tab, owner, refresh]);
 async function loadMore() {
  const version = generation.current; setMoreBusy(true);
  try {
   if (tab === 'users' && users?.pageInfo.endCursor) {
    const result = await query(AdminUsersDocument, { first: 20, after: users.pageInfo.endCursor });
    if (version === generation.current) setUsers(previous => ({ ...result.adminUsers, nodes: [...(previous?.nodes ?? []), ...result.adminUsers.nodes] }));
   } else if (tab === 'files' && files?.pageInfo.endCursor) {
    const result = await query(AdminFilesDocument, { first: 20, after: files.pageInfo.endCursor, owner: owner || null });
    if (version === generation.current) setFiles(previous => ({ ...result.adminFiles, nodes: [...(previous?.nodes ?? []), ...result.adminFiles.nodes] }));
   } else if (tab === 'audit' && audit?.pageInfo.endCursor) {
    const result = await query(AdminAuditDocument, { first: 20, after: audit.pageInfo.endCursor });
    if (version === generation.current) setAudit(previous => ({ ...result.adminAudit, nodes: [...(previous?.nodes ?? []), ...result.adminAudit.nodes] }));
   }
  } catch (failure) { setError(explainError(failure)); } finally { setMoreBusy(false); }
 }
 async function updateQuota() {
  if (!quotaTarget) return;
  setBusy(true); setError('');
  try { await mutate(SetQuotaDocument, { id: quotaTarget.id, bytes: quota }); setQuotaTarget(undefined); setNotice('Storage quota updated and recorded in the audit log.'); setRefresh(value => value + 1); }
  catch (failure) { setError(explainError(failure)); } finally { setBusy(false); }
 }
 async function updateStatus() {
  if (!statusTarget) return;
  setBusy(true); setError('');
  try { await mutate(SetDisabledDocument, { id: statusTarget.id, disabled: !statusTarget.disabledAt }); setStatusTarget(undefined); setNotice('Account status updated and recorded in the audit log.'); setRefresh(value => value + 1); }
  catch (failure) { setError(explainError(failure)); } finally { setBusy(false); }
 }
 const current = tab === 'users' ? users : tab === 'files' ? files : audit;
 return <>
  <div className="page-heading"><div><span className="eyebrow">ADMINISTRATOR WORKSPACE</span><h1>Administration</h1><p className="muted">Manage access and storage with a clear record of every change.</p></div><Button variant="secondary" disabled={loading} onClick={() => setRefresh(value => value + 1)}><RefreshCw size={16} />Refresh</Button></div>
  <div className="stats-grid admin-stats">
   <section className="stat-card"><span className="stat-icon"><Users size={20} /></span><span className="stat-label">Accounts</span><strong>{stats?.userCount ?? '—'}</strong><small>{stats?.fileCount ?? '—'} logical files</small></section>
   <section className="stat-card"><span className="stat-icon"><HardDrive size={20} /></span><span className="stat-label">Referenced content</span><strong>{stats ? formatBytes(stats.referencedBytes) : '—'}</strong><small>{stats ? formatBytes(stats.logicalBytes) : '—'} logical storage</small></section>
   <section className="stat-card"><span className="stat-icon"><ShieldCheck size={20} /></span><span className="stat-label">Deduplication savings</span><strong>{stats ? formatBytes(stats.savedBytes) : '—'}</strong><small>{stats?.savingsPercent ?? '—'}% saved across all accounts</small></section>
  </div>
  <div className="admin-summary"><span>{stats ? formatBytes(stats.pendingDeletionBytes) : '—'} awaiting cleanup</span><span>{stats?.downloadStarts ?? '—'} shared download starts</span><span>Content totals exclude temporary files and filesystem overhead.</span></div>
  {error && <Notice error>{error}</Notice>}{notice && <Notice>{notice}</Notice>}
  <section className="panel">
   <div className="tabs" role="tablist" aria-label="Administration sections">{([{ key: 'users', label: 'Users', Icon: UserRound }, { key: 'files', label: 'All files', Icon: Files }, { key: 'audit', label: 'Audit log', Icon: Activity }] as const).map(item => <button key={item.key} id={`tab-${item.key}`} type="button" role="tab" aria-selected={tab === item.key} aria-controls="admin-tabpanel" tabIndex={tab === item.key ? 0 : -1} onKeyDown={event => {
    if (event.key === 'ArrowRight' || event.key === 'ArrowLeft') { const order = ['users', 'files', 'audit'] as const; const next = order[(order.indexOf(tab) + (event.key === 'ArrowRight' ? 1 : 2)) % 3]; setTab(next); document.getElementById(`tab-${next}`)?.focus(); }
   }} onClick={() => setTab(item.key)}><item.Icon size={17} />{item.label}</button>)}</div>
   <div id="admin-tabpanel" role="tabpanel" aria-labelledby={`tab-${tab}`}>
    {tab === 'files' && <form className="owner-filter" onSubmit={event => { event.preventDefault(); setOwner(ownerInput.trim()); }}><label>Filter by uploader ID<input value={ownerInput} onChange={event => setOwnerInput(event.target.value)} placeholder="All uploaders" pattern="[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}" /></label><Button type="submit" variant="secondary">Apply</Button><Button type="button" variant="ghost" onClick={() => { setOwner(''); setOwnerInput(''); }}>Clear</Button></form>}
    {loading ? <div className="table-loading" role="status">Loading administration data…</div> : !current?.nodes.length ? <EmptyState title="Nothing to show">There are no matching records yet.</EmptyState> :
     <div className="table-scroll">{tab === 'users' ? <table><thead><tr><th>Account</th><th>Role / status</th><th>Storage</th><th>Actions</th></tr></thead><tbody>{users?.nodes.map(user => <tr key={user.id}>
      <td><strong>{user.loginName ?? 'Unprovisioned account'}</strong><small className="id-text">{user.id}</small></td><td><span className="badge">{user.role === UserRole.Admin ? 'Admin' : 'User'}</span><span className={`badge ${user.disabledAt ? 'badge-muted' : 'badge-green'}`}>{user.disabledAt ? 'Disabled' : 'Active'}</span></td><td className="nowrap">{formatBytes(user.usedBytes)}<small>of {formatBytes(user.quotaBytes)}</small></td>
      <td><div className="row-actions"><Button variant="ghost" onClick={event => { rememberFocus(event); setQuotaTarget(user); setQuota(user.quotaBytes); }} aria-label={`Edit quota for ${user.loginName ?? user.id}`}><Settings2 size={16} />Quota</Button><Button variant="ghost" disabled={user.role === UserRole.Admin} onClick={event => { rememberFocus(event); setStatusTarget(user); }}>{user.disabledAt ? 'Enable' : 'Disable'}</Button><Button variant="ghost" onClick={() => { setOwner(user.id); setOwnerInput(user.id); setTab('files'); }}>Files</Button></div></td>
     </tr>)}</tbody></table> : tab === 'files' ? <table><thead><tr><th>File</th><th>Uploader</th><th>Size</th><th>Shared download starts</th></tr></thead><tbody>{files?.nodes.map(item => <tr key={item.file.id}><td><strong>{item.file.name}</strong><small>{item.file.detectedMIME}</small></td><td>{item.loginName ?? 'Unprovisioned account'}<small className="id-text">{item.ownerId}</small></td><td>{formatBytes(item.file.sizeBytes)}</td><td>{item.downloadStarts}</td></tr>)}</tbody></table> :
      <table><thead><tr><th>Change</th><th>Target / actor</th><th>Details</th><th>When</th></tr></thead><tbody>{audit?.nodes.map(entry => <tr key={entry.id}><td><strong>{{ USER_QUOTA_CHANGED: 'Quota changed', USER_DISABLED: 'Account disabled', USER_ENABLED: 'Account enabled' }[entry.action] ?? entry.action}</strong><small>Event #{entry.id}</small></td><td><span className="id-text">Target: {entry.targetUserId}</span><small className="id-text">By: {entry.actorId}</small></td><td>{entry.action === 'USER_QUOTA_CHANGED' ? `${formatBytes(entry.previousQuota)} → ${formatBytes(entry.newQuota)}` : `${entry.revokedSessions} sessions, ${entry.revokedShares} links revoked`}</td><td className="nowrap muted">{formatDate(entry.occurredAt)}</td></tr>)}</tbody></table>}</div>}
    <div className="table-footer"><span>{current?.nodes.length ?? 0} records shown</span>{current?.pageInfo.hasNextPage && <Button variant="secondary" disabled={loading || moreBusy} onClick={() => void loadMore()}>{moreBusy ? 'Loading…' : 'Load more'}</Button>}</div>
   </div>
  </section>
  <Dialog open={Boolean(quotaTarget)} onClose={() => setQuotaTarget(undefined)} title="Change storage quota" description={quotaTarget?.loginName ?? quotaTarget?.id}>
   <form className="form-stack" onSubmit={event => { event.preventDefault(); void updateQuota(); }}><label>Quota in bytes<input autoFocus required inputMode="numeric" pattern="0|[1-9][0-9]*" value={quota} onChange={event => setQuota(event.target.value)} /></label><p className="help-text">Current usage: {formatBytes(quotaTarget?.usedBytes ?? '0')}. The new quota must cover existing files. 10 MB is 10,000,000 bytes.</p>{error && <Notice error>{error}</Notice>}<div className="dialog-actions"><Button type="button" variant="secondary" onClick={() => setQuotaTarget(undefined)}>Cancel</Button><Button disabled={busy} type="submit">{busy ? 'Saving…' : 'Save quota'}</Button></div></form>
  </Dialog>
  <Dialog open={Boolean(statusTarget)} onClose={() => setStatusTarget(undefined)} title={statusTarget?.disabledAt ? 'Enable this account?' : 'Disable this account?'} description={statusTarget?.disabledAt ? 'The user can sign in again. Revoked sessions and links will remain invalid.' : 'This signs the user out and revokes their sharing links. Their files will be retained.'}>
   <p className="delete-filename">{statusTarget?.loginName ?? statusTarget?.id}</p>{error && <Notice error>{error}</Notice>}<div className="dialog-actions"><Button variant="secondary" onClick={() => setStatusTarget(undefined)}>Cancel</Button><Button disabled={busy} variant={statusTarget?.disabledAt ? 'primary' : 'danger'} onClick={() => void updateStatus()}>{busy ? 'Saving…' : statusTarget?.disabledAt ? 'Enable account' : 'Disable account'}</Button></div>
  </Dialog>
 </>;
}
