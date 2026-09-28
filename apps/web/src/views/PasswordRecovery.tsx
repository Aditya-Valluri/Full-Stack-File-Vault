import { useState, type FormEvent } from 'react';
import { ChangePasswordDocument, CompletePasswordResetDocument, RequestPasswordResetDocument } from '../generated/graphql';
import { bootstrap, explainError, mutate, setCSRF } from '../lib/api';
import { Button, Notice } from '../components/ui';

export function PasswordRecovery({ mode, onBack }: { mode: 'reset' | 'change'; onBack: () => void }) {
 const [email, setEmail] = useState('');
 const [sent, setSent] = useState(false);
 const [done, setDone] = useState(false);
 const [code, setCode] = useState('');
 const [current, setCurrent] = useState('');
 const [password, setPassword] = useState('');
 const [confirm, setConfirm] = useState('');
 const [busy, setBusy] = useState(false);
 const [error, setError] = useState('');
 async function submit(event: FormEvent) {
  event.preventDefault(); setBusy(true); setError('');
  try {
   if (mode === 'reset') await bootstrap();
   if (mode === 'reset' && !sent) {
    await mutate(RequestPasswordResetDocument, { email }); setSent(true); return;
   }
   if (password !== confirm) { setError('Passwords must match.'); return; }
   if (mode === 'change') {
    await mutate(ChangePasswordDocument, { currentPassword: current, newPassword: password });
   } else {
    await mutate(CompletePasswordResetDocument, { code: code.trim(), password });
   }
   setCurrent(''); setPassword(''); setConfirm(''); setCode(''); setCSRF('');
   if (mode === 'change') { window.location.assign('/'); return; }
   setDone(true);
  } catch (failure) { setError(explainError(failure)); }
  finally { setBusy(false); }
 }
 if (done) return <section><h2>Password updated</h2><p>You have been signed out of all sessions. Sign in with your new password.</p><Button onClick={() => window.location.assign('/')}>Back to sign in</Button></section>;
 return <section className="login-form-wrap">
  <h2>{mode === 'change' ? 'Change password' : 'Reset password'}</h2>
  <p className="muted">{mode === 'change' ? 'Enter your current password and choose a different new password. You will be signed out of all sessions.' : sent ? 'Check your inbox for a 7-digit code. Only eligible verified email accounts can be reset. Use this browser; codes have a five-attempt limit and expire with your session or after 15 minutes.' : 'Enter your verified account email. Username-only accounts without a verified email cannot use email recovery.'}</p>
  <form className="form-stack" onSubmit={event => void submit(event)}>
   {mode === 'reset' && !sent ? <label>Email<input type="email" autoComplete="email" required maxLength={254} value={email} onChange={event => setEmail(event.target.value)} /></label> : <>
    {mode === 'change' ? <label>Current password<input type="password" autoComplete="current-password" required maxLength={1024} value={current} onChange={event => setCurrent(event.target.value)} /></label> : <label>Reset code (7 digits)<input type="text" inputMode="numeric" pattern="[0-9]{7}" autoComplete="one-time-code" required minLength={7} maxLength={7} value={code} onChange={event => setCode(event.target.value)} /></label>}
    <label>New password<input type="password" autoComplete="new-password" required minLength={15} maxLength={1024} value={password} onChange={event => setPassword(event.target.value)} /></label>
    <p className="muted">Use at least 15 characters, up to 1024 UTF-8 bytes. Common passwords, repeated patterns and obvious sequences are rejected.</p>
    <label>Confirm password<input type="password" autoComplete="new-password" required minLength={15} maxLength={1024} value={confirm} onChange={event => setConfirm(event.target.value)} /></label>
   </>}
   {error && <Notice error>{error}</Notice>}
   <Button type="submit" disabled={busy}>{busy ? 'Please wait…' : mode === 'change' ? 'Change password' : sent ? 'Reset password' : 'Send reset code'}</Button>
   <Button type="button" variant="ghost" disabled={busy} onClick={onBack}>{mode === 'change' ? 'Back to files' : 'Back to sign in'}</Button>
  </form>
 </section>;
}
