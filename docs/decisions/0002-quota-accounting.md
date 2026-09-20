# ADR 0002: Logical storage accounting

Status: Historical Step 1 working assumption. The derived-usage enforcement proposal
is superseded by [ADR 0005](0005-quota-and-publication.md); per-logical-file accounting
is retained. Neither enforcement approach is implemented yet.

## Context and decision

The default quota is 10,000,000 bytes (decimal MB). The supplied foundation script
permits higher per-user quotas and disallows negative quotas. This is a configurable
default, not a hard system-wide ceiling. This interpretation comes from the user's
script; the hiring-task PDF has not been independently verified.

Count the full blob size for each user-owned file reference. Two references to a
4 MB blob consume 8 MB of that user's logical quota, although physical storage is
4 MB. Another user's reference consumes that other user's quota independently.

The alternative is unique-content accounting per user. It avoids charging for
duplicate references but makes the effect of upload/deletion depend on whether
other references exist. Per-file accounting is easier to explain and calculate.

## Enforcement design for a later step

Initially derive usage by summing blob sizes joined through the user's files.
Compared with a cached usage counter, this avoids counter drift but costs an
aggregate query. A later optimization can introduce a transactionally maintained
counter if measurements justify it.

At READ COMMITTED isolation, every quota-affecting writer must first lock the same
user row, then calculate usage in a subsequent statement and change file references
within that transaction. Quota updates must use the same coordination. A plain
unlocked read followed by insertion permits concurrent uploads to exceed quota.
Do not carry this protocol unchanged to another isolation level without reviewing
snapshot behavior and retry requirements. Avoid holding locks while receiving an
upload; stage and hash bytes before the short publication transaction.

## Current boundary

`quota_bytes >= 0` validates the policy value only. Aggregate enforcement is not
implemented. `DEFAULT_USER_QUOTA_BYTES` in `.env.example` is currently unused;
changing it does not change the SQL default. Future application configuration must
explicitly supply a value when provisioning users or retain the database default.

Rate limiting is separate from storage quotas. Before implementing it, define
whether 2 calls/sec means a rolling window or minimum spacing and whether GraphQL
operations or HTTP requests are counted. No limiter or Redis service is added here.
