import { useEffect, useState, type FormEvent } from 'react';
import { ArrowLeft, ArrowRight, Eye, EyeOff, FolderLock, ShieldCheck } from 'lucide-react';
import { LoginDocument, EmailRegistrationEnabledDocument, type IdentityQuery } from '../generated/graphql';
import { errorCode, explainError, mutate, query, setCSRF } from '../lib/api';
import { Button, Notice, rememberFocus } from '../components/ui';

import { PasswordRecovery } from './PasswordRecovery';
import { Registration } from './Registration';
import { SignInHelp, ContactAdministrator } from './SignInHelp';

export function LoginView({ onLogin, onBack, error: initialError = '' }:
 { onLogin: (user: IdentityQuery['me']) => void; onBack?: () => void; error?: string }) {
 const [helping,setHelping]=useState(false);
 const [contacting,setContacting]=useState(false);
 const [resetEmail,setResetEmail]=useState('');
 const [resetSent,setResetSent]=useState(false);
 const [recovering, setRecovering] = useState(false);
 const [registering, setRegistering] = useState(false);
 const [registrationEnabled, setRegistrationEnabled] = useState(false);
 useEffect(() => {
  let active = true;
  void query(EmailRegistrationEnabledDocument, {}).then(result => { if (active) setRegistrationEnabled(result.emailRegistrationEnabled); }).catch(() => {});
  return () => { active = false; };
 }, []);
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
   setError(errorCode(failure) === 'UNAUTHENTICATED' ? 'The username, email or password was not accepted.' : explainError(failure));
  } finally { setBusy(false); }
 }
 return <main className="login-page">
  <section className="login-story">
   <a className="brand" href="/"><span className="brand-mark"><FolderLock size={24} /></span><span>Full Stack File Vault</span></a>
   <div><span className="eyebrow">A LITTLE MORE PEACE OF MIND</span><h1>Your files.<br />Your space.<br /><em>Your control.</em></h1><p>Keep your work organized, find what matters, and share only what you choose.</p></div>
   <div className="login-trust"><ShieldCheck size={20} /><span>Private by default. Shared on your terms.</span></div>
  </section>
  <section className="login-panel">
   <div className="login-form-wrap">
    {onBack && <Button variant="ghost" onClick={onBack}><ArrowLeft size={16} />Back to shared file</Button>}
    {helping ? <SignInHelp onBack={()=>setHelping(false)} /> : recovering ? <PasswordRecovery mode="reset" initialEmail={resetEmail} initialCodeSent={resetSent} onBack={() => setRecovering(false)} /> : registering ? <Registration onLogin={onLogin} onBack={() => setRegistering(false)} onReset={email=>{setResetEmail(email);setResetSent(true);setRegistering(false);setRecovering(true);}} /> : <>
    <span className="eyebrow">WELCOME BACK</span><h2>Sign in to your vault</h2><p className="muted">Use your Full Stack File Vault account to continue.</p>
    <form onSubmit={event => void submit(event)} className="form-stack">
     <label>Username or email<input autoComplete="username" required maxLength={254} value={loginName} onChange={event => setLoginName(event.target.value)} placeholder="Your username or verified email" /></label>
     <label>Password<div className="password-field"><input type={visible ? 'text' : 'password'} autoComplete="current-password" required maxLength={1024} value={password} onChange={event => setPassword(event.target.value)} placeholder="Your password" /><button type="button" aria-label={visible ? 'Hide password' : 'Show password'} onClick={() => setVisible(!visible)}>{visible ? <EyeOff size={18} /> : <Eye size={18} />}</button></div></label>
     {error && <Notice error>{error}</Notice>}
     <Button type="submit" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}<ArrowRight size={17} /></Button>
    </form>
    <div className="form-stack">
     <Button variant="secondary" disabled={!registrationEnabled} onClick={() => { setPassword(''); setRegistering(true); }}>New user? Create account</Button>
     {!registrationEnabled && <p className="muted">Account creation and email recovery are currently unavailable.</p>}
     {registrationEnabled && <Button variant="ghost" onClick={() => { setPassword(''); setResetEmail(''); setResetSent(false); setRecovering(true); }}>Forgot password?</Button>}
     <Button variant="ghost" onClick={()=>{setPassword('');setHelping(true);}}>Help signing in</Button>
     <Button variant="ghost" onClick={event=>{rememberFocus(event);setPassword('');setContacting(true);}}>Contact administrator</Button>
    </div>
    </>}
   </div>
  </section>
 {contacting && <ContactAdministrator open={contacting} onClose={()=>setContacting(false)} enabled={registrationEnabled} />}
 </main>;
}
