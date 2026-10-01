# Browser application

The React/TypeScript application lives in `apps/web`. Apollo Client sends all
application operations to `/graphql`, including GraphQL multipart uploads.
The browser retrieves authorized bytes through the short-lived content URLs.

For the consolidated setup and troubleshooting path, see [README quick-start](../../README.md#run-locally-windows--powershell).

## Development

Use Node 24 LTS, Go, and Docker Desktop. In a PowerShell terminal at the repository root:

```powershell
$env:PUBLIC_ORIGIN = 'http://127.0.0.1:5173'
./scripts/start-api.ps1
```

In a second terminal:

```powershell
cd apps/web
npm ci
npm run dev
```

Open http://127.0.0.1:5173. Vite proxies application and content requests to the
loopback API without changing the browser Origin. Legacy accounts can be provisioned
through the operator command. Verified email registration requires configured mail
delivery; there is no default password. See [email authentication](../authentication-email.md).

## Security and behavior

- Session cookies remain HttpOnly. CSRF tokens and share tokens stay in memory.
- Shared links carry their token in the fragment, which is removed from the
  address bar before requests. Reloading that page requires reopening the link.
- API authorization remains authoritative; hiding an admin screen is not a
  security boundary.
- Requests are serialized and paced within a tab. PostgreSQL enforces the
  two-per-second limit across tabs and API replicas.
- Development defaults allow ten files with a 20 MB file limit and 21 MB multipart
  request limit. The Render build uses a 10 MB file/batch UI limit and an 11 MB
  backend request limit for multipart overhead. Logical quota defaults to 10 MB
  in both environments; server limits and remaining quota remain authoritative.
- Network-ambiguous uploads offer an explicit same-key retry; see recovery.md.
- Previews follow the [server-approved image, TXT and PDF policy](previews.md).
- Upload progress reports transmitted request bytes; publication completion is separate.
  Atomic batches remain available. Individual uploads track each result and retry only
  unfinished files with their original idempotency keys. Tags publish transactionally.
- [Folders](folders.md) organize logical metadata without changing deduplication.
- [MFA](../authentication-mfa.md) requires a separately configured server encryption key.
- GraphQL bindings are generated from the Go schema and named frontend
  operations. Do not edit generated bindings directly.

## Validation

```powershell
npm run build
npm test
npx playwright install chromium
npm run test:e2e
```

Browser tests create their own PostgreSQL container, random credentials, temporary
storage, API process, and Vite process. Ports 18881 and 4173 must be available.
The tests do not use the development database. Test reports and screenshots are
ignored by Git. Automated accessibility checks supplement, rather than replace,
manual keyboard and assistive-technology review.

The account header displays the authenticated username and its initial rather than
a shortened UUID. The username comes from GraphQL login/current-user responses,
so existing sessions restore it after reload. A labeled Copy account ID action
retains recipient-sharing support without using internal IDs as the display name.
Long usernames truncate visually, with their full value available in the title.