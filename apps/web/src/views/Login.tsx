import { useState, type FormEvent } from 'react';
import { ArrowLeft, ArrowRight, Eye, EyeOff, FolderLock, ShieldCheck } from 'lucide-react';
import { LoginDocument, type IdentityQuery } from '../generated/graphql';
import { errorCode, explainError, mutate, setCSRF } from '../lib/api';
import { Button, Notice } from '../components/ui';

export function LoginView({ onLogin, onBack, error: initialError = '' }:
 { onLogin: (user: IdentityQuery['me']) => void; onBack?: () => void; error?: string }) {
 const [loginName, setLoginName] = useState('');
 const [password, setPassword] = useState('');
 const [visible, setVisible] = useState(false);
 const [busy, setBusy] = useState(false);
 const [error, setError] = useState(initialError);
 async function submit(event: FormEvent) {
  event.preventDefault(); setBusy(true); setError('');
  try {
   const result = await mutate(LoginDocument, { input: { loginName, password } });
   setCSRF(result.login.csrfToken); setPassword(''); onLogin(result.login.user);
  } catch (failure) {
   setError(errorCode(failure) === 'UNAUTHENTICATED' ? 'The username or password was not accepted.' : explainError(failure));
  } finally { setBusy(false); }
 }
 return <main className="login-page">
  <section className="login-story">
   <a className="brand" href="/"><span className="brand-mark"><FolderLock size={24} /></span><span>File Vault</span></a>
   <div><span className="eyebrow">A LITTLE MORE PEACE OF MIND</span><h1>Your files.<br />Your space.<br /><em>Your control.</em></h1><p>Keep your work organized, find what matters, and share only what you choose.</p></div>
   <div className="login-trust"><ShieldCheck size={20} /><span>Private by default. Shared on your terms.</span></div>
  </section>
  <section className="login-panel">
   <div className="login-form-wrap">
    {onBack && <Button variant="ghost" onClick={onBack}><ArrowLeft size={16} />Back to shared file</Button>}
    <span className="eyebrow">WELCOME BACK</span><h2>Sign in to your vault</h2><p className="muted">Use your File Vault account to continue.</p>
    <form onSubmit={event => void submit(event)} className="form-stack">
     <label>Username<input autoComplete="username" required maxLength={64} value={loginName} onChange={event => setLoginName(event.target.value)} placeholder="Your username" /></label>
     <label>Password<div className="password-field"><input type={visible ? 'text' : 'password'} autoComplete="current-password" required maxLength={1024} value={password} onChange={event => setPassword(event.target.value)} placeholder="Your password" /><button type="button" aria-label={visible ? 'Hide password' : 'Show password'} onClick={() => setVisible(!visible)}>{visible ? <EyeOff size={18} /> : <Eye size={18} />}</button></div></label>
     {error && <Notice error>{error}</Notice>}
     <Button type="submit" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}<ArrowRight size={17} /></Button>
    </form>
    <p className="login-help">Need an account or help signing in?<br />Contact your administrator.</p>
   </div>
  </section>
 </main>;
}
