import { lazy, Suspense, useEffect, useState } from 'react';
import { Archive, ArrowUpRight, Copy, FolderLock, LogOut, ShieldCheck } from 'lucide-react';
import { bootstrap, client, errorCode, explainError, mutate, query, setAuthenticated, setCSRF } from './lib/api';
import { IdentityDocument, LogoutDocument, UserRole, type IdentityQuery } from './generated/graphql';
import { Button, Notice } from './components/ui';
import { LoginView } from './views/Login';
import { VaultView } from './views/Vault';
const SharedView = lazy(() => import('./views/Shared').then(module => ({ default: module.SharedView })));
const AdminView = lazy(() => import('./views/Admin').then(module => ({ default: module.AdminView })));

type Identity = IdentityQuery['me'];
// Capture once before removing the bearer token from browser history.
const isSharedRoute = window.location.pathname === '/share';
const initialShareToken = isSharedRoute ? window.location.hash.slice(1) : '';
if (isSharedRoute && window.location.hash) window.history.replaceState(null, '', '/share');

export function App() {
 const [identity, setIdentity] = useState<Identity | null>();
 const [sharedLink, setSharedLink] = useState({ token: initialShareToken, revision: 0 });
 useEffect(() => {
  function reopenShare() {
   if (!isSharedRoute || !window.location.hash) return;
   const token = window.location.hash.slice(1);
   window.history.replaceState(null, '', '/share');
   setSharedLink(previous => ({ token, revision: previous.revision + 1 }));
  }
  window.addEventListener('hashchange', reopenShare);
  return () => window.removeEventListener('hashchange', reopenShare);
 }, []);
 const [tab, setTab] = useState<'vault' | 'admin'>('vault');
 const [showLogin, setShowLogin] = useState(false);
 const [error, setError] = useState('');
 const [busy, setBusy] = useState(false);
 const [copied, setCopied] = useState(false);

 useEffect(() => {
  let active = true;
  void bootstrap().then(() => query(IdentityDocument, {})).then(
   data => { if (active) { setAuthenticated(true); setIdentity(data.me); } },
   failure => {
    if (!active) return;
    setIdentity(null);
    if (errorCode(failure) !== 'UNAUTHENTICATED') setError(explainError(failure));
   },
  );
  return () => { active = false; };
 }, []);

 useEffect(() => {
  function expired() {
   setIdentity(undefined); setError('Your session ended. Please sign in again.');
   void bootstrap().catch(failure => setError(explainError(failure))).finally(() => setIdentity(null));
  }
  window.addEventListener('vault:session-expired', expired);
  return () => window.removeEventListener('vault:session-expired', expired);
 }, []);
 async function logout() {
  setBusy(true); setError('');
  try {
   await mutate(LogoutDocument, {});
   setAuthenticated(false); setCSRF(''); await client.clearStore(); setIdentity(null); setTab('vault');
   await bootstrap();
  } catch (failure) { setError(explainError(failure)); }
  finally { setBusy(false); }
 }
 function signedIn(user: Identity) { setAuthenticated(true); setIdentity(user); setShowLogin(false); setError(''); }
 if (identity === undefined) return <div className="loading-screen" role="status"><ShieldCheck size={34} /><p>Opening Full Stack File Vault…</p></div>;
 if ((!identity && !isSharedRoute) || showLogin) return <LoginView onLogin={signedIn} error={error} onBack={isSharedRoute ? () => setShowLogin(false) : undefined} />;

 return <div className="app-shell">
  <a className="skip-link" href="#main-content">Skip to content</a>
  <aside className="sidebar">
   <a className="brand" href="/"><span className="brand-mark"><FolderLock size={23} /></span><span>Full Stack File Vault<small>YOUR PRIVATE WORKSPACE</small></span></a>
   <div className="nav-caption">WORKSPACE</div>
   <nav aria-label="Main navigation">
    <button className={tab === 'vault' && !isSharedRoute ? 'nav-item active' : 'nav-item'} onClick={() => { if (isSharedRoute) window.location.assign('/'); else setTab('vault'); }}><Archive size={18} />My files</button>
    {identity?.role === UserRole.Admin && <button className={tab === 'admin' ? 'nav-item active' : 'nav-item'} onClick={() => { if (isSharedRoute) window.location.assign('/'); else setTab('admin'); }}><ShieldCheck size={18} />Administration</button>}
   </nav>
   <div className="sidebar-note"><ShieldCheck size={20} /><strong>Private by default</strong><p>You choose what to share. Revoke access whenever you need.</p></div>
   <div className="sidebar-footer"><span className="status-dot" />Full Stack File Vault workspace</div>
  </aside>
  <div className="workspace">
   <header className="topbar">
    <div className="breadcrumb">Workspace <span>/</span> <strong>{isSharedRoute ? 'Shared file' : tab === 'admin' ? 'Administration' : 'My files'}</strong></div>
    <div className="account">
     {identity ? <>
      <button className="account-id" aria-label="Copy account ID for sharing" title="Copy your account ID for recipient-specific sharing" onClick={() => {
       void navigator.clipboard.writeText(identity.id).then(() => { setCopied(true); setTimeout(() => setCopied(false), 2000); }, () => setError('Your browser could not copy the account ID.'));
      }}><span className="avatar" aria-hidden="true">{(identity.loginName || 'U').charAt(0).toUpperCase()}</span><span><span className="account-name" title={identity.loginName || 'My account'}>{identity.loginName || 'My account'}</span><small aria-live="polite">{copied ? 'Account ID copied' : 'Copy account ID'} <Copy size={10} aria-hidden="true" /></small></span></button>
      <Button variant="ghost" onClick={() => void logout()} disabled={busy} aria-label="Sign out"><LogOut size={17} /></Button>
     </> : <Button variant="secondary" onClick={() => setShowLogin(true)}>Sign in <ArrowUpRight size={15} /></Button>}
    </div>
   </header>
   <main id="main-content" className="main-content">
    {error && <Notice error>{error}</Notice>}
    <Suspense fallback={<p role="status">Loading workspace...</p>}>{isSharedRoute ? <SharedView key={`${identity?.id ?? 'anonymous'}:${sharedLink.revision}`} initialToken={sharedLink.token} signedIn={Boolean(identity)} onSignIn={() => setShowLogin(true)} />
     : tab === 'admin' && identity?.role === UserRole.Admin ? <AdminView key={identity.id} /> : <VaultView key={identity?.id} />}</Suspense>
   </main>
   <footer className="workspace-footer">Full Stack File Vault<span>Private files. Deliberate sharing.</span></footer>
  </div>
 </div>;
}
