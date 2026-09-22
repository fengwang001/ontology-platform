// Package version provides monotonic cache-data version numbers.
// The zero value means "no version".
package version

import (
	"fmt"
	"sync"
)

// Version is a monotonically increasing data version.
// The zero value (None) means "no version" and is older than
// any allocated version.
type Version uint64

// None is the zero version: "no version".
const None = Version(0)

// IsZero reports whether v is the zero ("no version") value.
func (v Version) IsZero() bool { return v == None }

// Before reports whether v is strictly older than o.
func (v Version) Before(o Version) bool { return v < o }

// After reports whether v is strictly newer than o.
func (v Version) After(o Version) bool { return v > o }

// Equal reports whether v and o are the same version.
func (v Version) Equal(o Version) bool { return v == o }

// String renders the version for logs and tests.
func (v Version) String() string {
	if v == None {
		return "none"
	}
	return fmt.Sprintf("v%d", uint64(v))
}

// Allocator hands out strictly increasing versions, starting at 1.
// It is safe for concurrent use.
type Allocator struct {
	mu   sync.Mutex
	next uint64
}

// NewAllocator returns an Allocator whose first Next call returns 1.
func NewAllocator() *Allocator { return &Allocator{} }

// Next allocates and returns the next monotonic version.
func (a *Allocator) Next() Version {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.next++
	return Version(a.next)
}

// Current returns the most recently allocated version,
// or None if Next was never called.
func (a *Allocator) Current() Version {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Version(a.next)
}
