package upload

import "context"

// PublicationStore separates byte storage from transactional ownership. Prepare
// completes expensive copying/hashing outside DB locks; Promote must be bounded.
// Implementations must not replace an existing generation or delete published keys.
type PublicationStore interface {
	Prepare(context.Context, *Staged) (PreparedObject, error)
	Verify(context.Context, string, int64) error
}

type PreparedObject interface {
	Promote(context.Context, string) error
	Close() error
}
