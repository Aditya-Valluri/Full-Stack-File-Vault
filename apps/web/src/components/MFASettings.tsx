import { useEffect, useState, type FormEvent } from 'react';
import { QRCodeSVG } from 'qrcode.react';
import { DisableMfaDocument, EnableMfaDocument, MfaStatusDocument, SetupMfaDocument } from '../generated/graphql';
import { explainError, mutate, query, setCSRF } from '../lib/api';
import { Button, Notice } from './ui';

export function MFASettings() {
 const [enabled, setEnabled] = useState(false);
 const [available, setAvailable] = useState(false);
 const [loading, setLoading] = useState(true);
 const [password, setPassword] = useState('');
 const [code, setCode] = useState('');
 const [uri, setURI] = useState('');
 const [recovery, setRecovery] = useState<string[]>([]);
 const [busy, setBusy] = useState(false);
 const [error, setError] = useState('');
 useEffect(() => {
  let active = true;
  void query(MfaStatusDocument, {}).then(result => { if (active) { setEnabled(result.mfaStatus.enabled); setAvailable(result.mfaStatus.available); } })
   .catch(failure => { if (active) setError(explainError(failure)); }).finally(() => { if (active) setLoading(false); });
  return () => { active = false; };
 }, []);
 async function submit(event: FormEvent) {
  event.preventDefault(); if (busy) return;
  setBusy(true); setError('');
  try {
   if (enabled) {
    await mutate(DisableMfaDocument, { password, code });
    setPassword(''); setCode(''); setCSRF(''); window.location.assign('/');
   } else if (uri) {
    const result = await mutate(EnableMfaDocument, { password, code });
    setRecovery(result.enableMFA.recoveryCodes); setURI(''); setPassword(''); setCode(''); setCSRF('');
   } else {
    const result = await mutate(SetupMfaDocument, { password });
    setURI(result.setupMFA.uri); setCode('');
   }
  } catch (failure) { setError(explainError(failure)); }
  finally { setBusy(false); }
 }
 if (recovery.length) return <section className="panel form-stack" aria-label="MFA recovery codes"><h2>Save your recovery codes</h2><p>MFA is enabled and all sessions are signed out. Save these one-time codes in your password manager. They will not be shown again.</p><ul>{recovery.map(value => <li key={value}><code>{value}</code></li>)}</ul><p>Each recovery code replaces the authenticator code for one sign-in. Your password is still required.</p><Button onClick={() => { setRecovery([]); window.location.assign('/'); }}>I saved my codes — sign in</Button></section>;
 return <section className="panel form-stack" aria-label="Authenticator security"><h2>Authenticator security</h2>
  {loading ? <p role="status">Loading authenticator settings…</p> : <>
   <p>{enabled ? 'MFA is enabled. Sign-in requires your password and a fresh authenticator or recovery code.' : 'Add an authenticator app as a second sign-in factor.'}</p>
   {!available && !enabled ? <p>Authenticator enrollment is not configured on this server. Contact your administrator.</p> :
    <form className="form-stack" onSubmit={event => void submit(event)}>
     <label>Current password for MFA<input type="password" autoComplete="current-password" required maxLength={1024} value={password} onChange={event => setPassword(event.target.value)} /></label>
     {uri && <><QRCodeSVG value={uri} size={200} marginSize={4} title="Scan with your authenticator app" /><label>Manual setup key<input readOnly value={new URL(uri).searchParams.get('secret') ?? ''} /></label><p className="help-text">Scan this QR code with your authenticator app. Setup expires in 10 minutes. Never send this key to anyone.</p></>}
     {(enabled || uri) && <label>{enabled ? 'Authenticator or recovery code' : 'Authenticator code (6 digits)'}<input autoComplete="one-time-code" required maxLength={40} value={code} onChange={event => setCode(event.target.value)} /></label>}
     <p className="help-text">Enabling or disabling MFA signs out every session. A code can be used once; wait for a fresh code if you just signed in.</p>
     <Button type="submit" disabled={busy}>{busy ? 'Checking…' : enabled ? 'Disable MFA' : uri ? 'Confirm and enable MFA' : 'Set up authenticator'}</Button>
    </form>}
  </>}
  {error && <Notice error>{error}</Notice>}
 </section>;
}
