package cascade

type Policy int

const (
	// Background removes an object and marks ownership-orphaned dependents for deletion.
	Background Policy = iota + 1
	// Foreground propagates deletion through dependents whose other owners are gone or deleting.
	Foreground
	// Orphan removes only the target and preserves dependents after owner edges detach.
	Orphan
)

// OwnerRef is one owner reference and its deletion-blocking behavior.
type OwnerRef struct {
	OwnerID       string
	BlockDeletion bool
}

// CreateObjectInput describes a new object.
type CreateObjectInput struct {
	ID         string
	Owners     []OwnerRef
	Finalizers []string
}

// ObjectState is a canonical snapshot of one live object.
type ObjectState struct {
	ID         string
	Owners     []OwnerRef
	Finalizers []string
	Deleting   bool
	Policy     Policy
	DeleteAt   int64
}

// OperationResult reports the synchronized outcome of one mutating operation.
type OperationResult struct {
	ObjectID string
	Removed  []string
	Upgraded bool
	NoChange bool
	Stats    Stats
}

// Stats counts objects and references touched by local convergence.
type Stats struct {
	ObjectsVisited int
	ReferencesUsed int
}
