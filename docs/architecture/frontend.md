# Browser application

The React/TypeScript application lives in `apps/web`. Apollo Client sends all
application operations to `/graphql`, including GraphQL multipart uploads.
The browser retrieves authorized bytes through the short-lived content URLs.

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
loopback API without changing the browser Origin. Provision accounts through the
existing operator command; there is no public registration or default password.

## Security and behavior

- Session cookies remain HttpOnly. CSRF tokens and share tokens stay in memory.
- Shared links carry their token in the fragment, which is removed from the
  address bar before requests. Reloading that page requires reopening the link.
- API authorization remains authoritative; hiding an admin screen is not a
  security boundary.
- Requests are serialized and paced within a tab. PostgreSQL enforces the
  two-per-second limit across tabs and API replicas.
- Uploads currently use the default ten-file, 20 MB request/file limits and
  show the user's remaining logical quota. Server limits remain authoritative.
- Network-ambiguous uploads offer an explicit same-key retry; see recovery.md.
- Previews are restricted to the server-approved raster image types.
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