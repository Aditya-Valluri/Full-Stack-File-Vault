# ADR 0003: GraphQL is the single application API

Status: Accepted by the user. Reconsider only for a demonstrated implementation blocker.

All application operations use gqlgen at `/graphql`: authentication, me, files,
search, uploadFile, uploadFiles, deleteFile, sharing, quota, storageStats and admin.
These are GraphQL fields/operation areas, not nested HTTP routes. Final field names
and types will be designed in their respective implementation steps.

Use gqlgen multipart uploads. Do not add a parallel `/api/upload` endpoint or REST
application API. Multipart limits, CSRF protection and operation-aware rate limiting
must be addressed before enabling uploads; they are not implemented in Step 2.

GraphQL authorizes downloads/previews and returns an opaque, short-lived URL. Normal
HTTP retrieves bytes; it is file transport, not a second application API. Capability
URLs require expiry and must not appear in request logs. Transport is deferred.

Compared with maintaining both REST and GraphQL, this avoids duplicated contracts,
validation and authorization paths. The trade-off is GraphQL-specific handling of
uploads, query complexity, batching and rate limits. This decision is settled.

chi hosts HTTP routing; gqlgen owns the application schema and resolvers. `/healthz`
and `/readyz` are operational probes and do not expose application operations.

Implementation order: database foundation; Go/chi/pgxpool; gqlgen; authentication and
authorization; multipart uploads; deduplication and quota; file queries/search/delete;
sharing/stats/admin; frontend; CI/Kubernetes/UAT. Advance one step at a time.
