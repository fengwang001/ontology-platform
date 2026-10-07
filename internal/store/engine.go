package store

import "errors"

// ErrNotFound indicates a key does not exist in the engine.
var ErrNotFound = errors.New("store: key not found")

// ErrUncertain indicates an object participates in a batch whose final
// outcome has not yet been externally determined. Reads and writes are
// rejected while the batch remains in that uncertainty window.
var ErrUncertain = errors.New("store: object state is uncertain due to an in-flight batch")

// ErrBusy indicates that another batch is currently in progress.
var ErrBusy = errors.New("store: another batch is in progress")

// CrashError is returned by a fault hook to simulate process death at a
// named barrier. It must be treated as fatal to the Store instance.
type CrashError struct{ Barrier string }

func (e *CrashError) Error() string { return "simulated crash at " + e.Barrier }

// CrashHook is invoked at every durability barrier. Returning a CrashError
// simulates process interruption at that exact point.
type CrashHook func(barrier string) error

// Engine is the durable key/value substrate. Every Put is a durability
// barrier: implementations must flush to stable storage before returning.
// Rename atomically replaces key dst with the current contents of key src.
type Engine interface {
	Get(key string) ([]byte, error)
	Put(key string, value []byte) error
	Rename(src, dst string) error
	Delete(key string) error
}

// Lister is an optional engine capability used to replay the append-only
// audit journal. It is never used by the recovery decision itself.
type Lister interface {
	ListKeys(prefix string) []string
}

// Stats is implemented by engines that can report how many records a
// recovery scan inspected. It backs the bounded-recovery evidence
// requirement and counts access to batch-owned records only; it never
// depends on the total number of historical batches.
type Stats interface {
	RecordAccesses() int
}
