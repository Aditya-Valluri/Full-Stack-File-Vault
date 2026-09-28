import { useState, type FormEvent } from 'react';
import { ContactAdministratorDocument } from '../generated/graphql';
import { bootstrap, explainError, mutate } from '../lib/api';
import { Button, Dialog, Notice } from '../components/ui';

export function SignInHelp({ onBack }: { onBack: () => void }) {
 return <section className="form-stack">
  <h2>Help signing in</h2>
  <h3>How do I create an account?</h3>
  <p>Choose “New user? Create account”, enter your email, and request a verification code. Enter the seven-digit code in the same browser, then choose and confirm your password.</p>
  <h3>What password should I use?</h3>
  <p>Use at least 15 characters. Common passwords and obvious patterns are rejected. A unique passphrase or password manager is recommended.</p>
  <h3>I forgot my password</h3>
  <p>Choose “Forgot password?” on the sign-in screen. Enter your verified account email and request a seven-digit reset code. Use that code in the same browser to choose a new password, then sign in again. This resets your File Vault password, not your Gmail password. Username-only accounts need administrator help.</p>
  <h3>My code or sign-in is not working</h3>
  <p>Check your spam folder. Codes require the browser that requested them and expire with that session or after 15 minutes. Five attempts exhaust a code. If you are rate limited, wait up to 15 minutes before trying again. Use the same site address throughout.</p>
  <h3>Who sends the emails?</h3>
  <p>File Vault uses its dedicated project Gmail sender. It does not need access to your inbox. Never send passwords or verification codes to support.</p>
  <Button variant="secondary" onClick={onBack}>Back to sign in</Button>
 </section>;
}

export function ContactAdministrator({ open, onClose, enabled }: { open: boolean; onClose: () => void; enabled: boolean }) {
 const [subject,setSubject]=useState('');
 const [message,setMessage]=useState('');
 const [busy,setBusy]=useState(false);
 const [sent,setSent]=useState(false);
 const [error,setError]=useState('');
 async function submit(event: FormEvent) {
  event.preventDefault();setBusy(true);setError('');
  try {
   await bootstrap();
   await mutate(ContactAdministratorDocument,{subject:subject.trim(),message:message.trim()});
   setSent(true);setSubject('');setMessage('');
  } catch(failure){setError(explainError(failure));}
  finally{setBusy(false);}
 }
 return <Dialog open={open} onClose={onClose} title="Contact administrator" description="Your message goes to the project support inbox. Do not include passwords, verification codes, or private files.">
  {!enabled ? <Notice>Contact delivery is currently unavailable. Please try again later.</Notice> : sent ? <Notice>Your message was accepted for delivery to the administrator.</Notice> :
   <form className="form-stack" onSubmit={event=>void submit(event)}>
    <label>Subject<input required maxLength={120} value={subject} onChange={event=>setSubject(event.target.value)} /></label>
    <label>Message<textarea required rows={6} maxLength={4000} value={message} onChange={event=>setMessage(event.target.value)} /></label>
    <p className="muted">Include a reply email address in your message if you want a response. Subject: up to 120 UTF-8 bytes; message: up to 4000 UTF-8 bytes.</p>
    {error && <Notice error>{error}</Notice>}
    <Button type="submit" disabled={busy}>{busy?'Sending…':'Send message'}</Button>
   </form>}
 </Dialog>;
}
