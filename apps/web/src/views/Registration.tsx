import { useState, type FormEvent } from 'react';
import { CompleteEmailRegistrationDocument, RequestEmailRegistrationDocument, type IdentityQuery } from '../generated/graphql';
import { bootstrap, explainError, mutate, setCSRF } from '../lib/api';
import { Button, Notice } from '../components/ui';

export function Registration({ onLogin, onBack }: { onLogin: (user: IdentityQuery['me']) => void; onBack: () => void }) {
 const [email, setEmail] = useState('');
 const [sent, setSent] = useState(false);
 const [code, setCode] = useState('');
 const [password, setPassword] = useState('');
 const [confirmation, setConfirmation] = useState('');
 const [busy, setBusy] = useState(false);
 const [error, setError] = useState('');
 async function submit(event: FormEvent) {
  event.preventDefault(); setBusy(true); setError('');
  try {
   // Verification can outlive the ten-minute anonymous session. Bootstrap
   // refreshes expired state. Codes are bound to the original session; if it
   // expired, request a new code. No bearer token enters JavaScript.
   await bootstrap();
   if (!sent) {
    await mutate(RequestEmailRegistrationDocument, { email }); setSent(true);
   } else {
    if (password !== confirmation) { setError('Passwords must match.'); return; }
    const result = await mutate(CompleteEmailRegistrationDocument, { code: code.trim(), password });
    setCSRF(result.completeEmailRegistration.csrfToken);
    setPassword(''); setConfirmation(''); setCode('');
    onLogin(result.completeEmailRegistration.user);
   }
  } catch (failure) { setError(explainError(failure)); }
  finally { setBusy(false); }
 }
 return <>
  <h2>{sent ? 'Verify your email' : 'Create your account'}</h2>
  <p className="muted">{sent ? 'Enter the 7-digit code from your inbox in this browser. You have up to 5 attempts. If your session expires, request a new code. If you already have an account, sign in instead.' : 'Verify your email first, then choose a password to activate your account.'}</p>
  <form className="form-stack" onSubmit={event => void submit(event)}>
   {!sent ? <label>Email<input type="email" autoComplete="email" maxLength={254} required value={email} onChange={event => setEmail(event.target.value)} /></label> : <>
    <label>Verification code (7 digits)<input type="text" inputMode="numeric" pattern="[0-9]{7}" autoComplete="one-time-code" required minLength={7} maxLength={7} value={code} onChange={event => setCode(event.target.value)} /></label>
    <label>New password<input type="password" autoComplete="new-password" required minLength={15} maxLength={1024} value={password} onChange={event => setPassword(event.target.value)} /></label>
    <p className="muted">Use at least 15 characters (up to 1024 UTF-8 bytes). Common passwords, repeated patterns and obvious sequences are rejected. Try a unique passphrase of unrelated words.</p>
    <label>Confirm password<input type="password" autoComplete="new-password" required minLength={15} maxLength={1024} value={confirmation} onChange={event => setConfirmation(event.target.value)} /></label>
   </>}
   {error && <Notice error>{error}</Notice>}
   <Button type="submit" disabled={busy}>{busy ? 'Please wait…' : sent ? 'Verify and create account' : 'Send verification code'}</Button>
   {sent && <Button type="button" variant="ghost" disabled={busy} onClick={() => { setSent(false); setCode(''); setPassword(''); setConfirmation(''); setError(''); }}>Use another email or request a new code</Button>}
   <Button type="button" variant="ghost" disabled={busy} onClick={onBack}>Back to sign in</Button>
  </form>
 </>;
}
