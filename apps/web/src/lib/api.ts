import { ApolloClient, ApolloLink, InMemoryCache, type OperationVariables } from '@apollo/client';
import { CombinedGraphQLErrors } from '@apollo/client/errors';
import type { TypedDocumentNode } from '@graphql-typed-document-node/core';
import { print } from 'graphql';
import { Observable } from 'rxjs';
import { BootstrapDocument, IdentityDocument, type IdentityQuery } from '../generated/graphql';
import { requests } from './scheduler';

let csrfToken = '';
let authenticated = false;
export function setAuthenticated(value: boolean) { authenticated = value; }
let bootstrapPending: Promise<void> | undefined;
export function setCSRF(token: string) { csrfToken = token; }

const link = new ApolloLink(operation => new Observable(observer => {
 const controller = new AbortController();
 void requests.run(async () => {
  const context = operation.getContext();
  const headers = new Headers();
  if (context.bootstrap) headers.set('X-Vault-CSRF-Bootstrap', '1');
  else if (csrfToken) headers.set('X-CSRF-Token', csrfToken);
  const uploads = context.uploads as File[] | undefined;
  const variables = { ...operation.variables };
  const envelope = { query: print(operation.query), operationName: operation.operationName, variables };
  let body: BodyInit;
  if (uploads) {
   const many = Boolean(context.uploadMany);
   if (many) variables.files = uploads.map(() => null);
   else variables.file = null;
   const form = new FormData();
   form.append('operations', JSON.stringify(envelope));
   form.append('map', JSON.stringify(Object.fromEntries(uploads.map((_, i) =>
    [String(i), [many ? `variables.files.${i}` : 'variables.file']]))));
   uploads.forEach((file, i) => form.append(String(i), file, file.name));
   body = form; // Browser supplies the multipart boundary.
  } else {
   headers.set('Content-Type', 'application/json');
   body = JSON.stringify(envelope);
  }
  const response = await fetch('/graphql', {
   method: 'POST', credentials: 'same-origin', cache: 'no-store',
   headers, body, signal: controller.signal,
  });
  const type = response.headers.get('content-type') ?? '';
  if (!type.includes('json')) throw new Error('The service is unavailable. Please try again.');
  const result = await response.json() as ApolloLink.Result;
  const errors = 'errors' in result ? result.errors : undefined;
  if (authenticated && errors?.some(error => error.extensions?.code === 'UNAUTHENTICATED')) {
   authenticated = false; csrfToken = '';
   window.dispatchEvent(new Event('vault:session-expired'));
  }
  if (!response.ok && !('errors' in result)) throw new Error('The service is unavailable. Please try again.');
  return result;
 }, controller.signal).then(result => { observer.next(result); observer.complete(); }, error => observer.error(error));
 return () => controller.abort();
}));

export const client = new ApolloClient({
 link, cache: new InMemoryCache(), devtools: { enabled: false },
 defaultOptions: { query: { fetchPolicy: 'no-cache' }, mutate: { fetchPolicy: 'no-cache' } },
});
export async function query<D, V extends OperationVariables>(document: TypedDocumentNode<D, V>, variables: V): Promise<D> {
 const result = await client.query({ query: document, variables, fetchPolicy: 'no-cache' });
 if (!result.data) throw new Error('No response received.');
 return result.data as D;
}
export async function mutate<D, V extends OperationVariables>(document: TypedDocumentNode<D, V>, variables: V, context?: Record<string, unknown>): Promise<D> {
 const result = await client.mutate({ mutation: document, variables, context, fetchPolicy: 'no-cache' });
 if (!result.data) throw new Error('No response received.');
 return result.data as D;
}
export function bootstrap(): Promise<void> {
 bootstrapPending ??= mutate(BootstrapDocument, {}, { bootstrap: true })
  .then(result => { csrfToken = result.beginSession.csrfToken; })
  .finally(() => { bootstrapPending = undefined; });
 return bootstrapPending;
}
let identityPending: Promise<IdentityQuery> | undefined;
/** A reload resets local request pacing, but not the server's rolling user window.
 * Retry only session restoration on admission rejection; never replay uploads or
 * other mutations. Share the restoration across React StrictMode effect mounts. */
export function restoreIdentity(): Promise<IdentityQuery> {
 identityPending ??= (async () => {
  for (let attempt = 0; ; attempt++) {
   try {
    await bootstrap();
    return await query(IdentityDocument, {});
   } catch (error) {
    if (errorCode(error) !== 'RATE_LIMITED' || attempt >= 2) throw error;
    await new Promise(resolve => setTimeout(resolve, 1000));
   }
  }
 })().finally(() => { identityPending = undefined; });
 return identityPending;
}

export function errorCode(error: unknown): string | undefined {
 if (CombinedGraphQLErrors.is(error)) return error.errors[0]?.extensions?.code as string | undefined;
 return undefined;
}
export function explainError(error: unknown): string {
 const code = errorCode(error);
 if (code === 'LOGIN_TEMPORARILY_BLOCKED' && CombinedGraphQLErrors.is(error)) {
  const seconds = Number(error.errors[0]?.extensions?.retryAfterSeconds);
  const minutes = Number.isFinite(seconds) ? Math.max(1, Math.min(60, Math.ceil(seconds / 60))) : 5;
  return `Too many unsuccessful sign-in attempts. Try again in ${minutes} minute${minutes === 1 ? '' : 's'} or reset your password.`;
 }
 const messages: Record<string, string> = {
  UNAUTHENTICATED: 'Please sign in again to continue.',
  FORBIDDEN: 'You do not have permission to do that.',
  NOT_FOUND: 'This file or link is no longer available to this account.',
  CONTACT_INVALID: 'Enter a subject up to 120 UTF-8 bytes and a message up to 4000 UTF-8 bytes, without control characters.',
  RATE_LIMITED: 'Too many requests. Please try again later; authentication limits can last up to 15 minutes.',
  UPLOAD_RETRY_CONFLICT: 'This retry key belongs to a different upload. Re-select your files to start a new upload.',
  QUOTA_EXCEEDED: 'There is not enough space in your vault for these files.',
  DEMO_CAPACITY_REACHED: 'Storage capacity has been reached. Contact your administrator.',
  CONFLICT: 'That change conflicts with the current account state. Quota must cover existing files.',
  PASSWORD_REJECTED: 'Your current password was not accepted. Your password has not changed.',
  RESET_REJECTED: 'The reset code is invalid, expired, or no longer usable. Return to sign in to start a new reset request.',
  WEAK_PASSWORD: 'Use at least 15 characters. Avoid common passwords, repeated patterns, and obvious sequences. Try a unique passphrase of unrelated words.',
  REGISTRATION_REJECTED: 'Registration could not be completed. The code may be incorrect, expired, or no longer usable. Request a new code, or sign in or reset your password if you already have an account.',
  INVALID_INPUT: 'Check the values you entered and try again.',
  UNSUPPORTED_MEDIA_TYPE: 'This file cannot be previewed. You can download it instead.',
 };
 return code && messages[code] ? messages[code] : 'Something went wrong. Please try again.';
}
function safeContentURL(url: string): string {
 if (!/^\/(?:shared-)?content\/[A-Za-z0-9_-]{43}$/.test(url)) throw new Error('Invalid content URL.');
 return url;
}
export function startDownload(url: string, name: string): Promise<void> {
 return requests.run(async () => {
  const anchor = document.createElement('a');
  anchor.href = safeContentURL(url); anchor.download = name; anchor.rel = 'noreferrer';
  document.body.append(anchor); anchor.click(); anchor.remove();
 });
}
export function preparePreview(url: string): Promise<string> {
 return requests.run(async () => safeContentURL(url));
}
