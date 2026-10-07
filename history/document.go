package history

import "time"

// DocStatus is the lifecycle state of a document.
type DocStatus int

const (
	DocActive DocStatus = iota
	DocCached
	DocUnloaded
)

func (s DocStatus) String() string {
	switch s {
	case DocActive:
		return "active"
	case DocCached:
		return "cached"
	default:
		return "unloaded"
	}
}

// Flag is one of the four cache-eligibility conditions of a document.
type Flag int

const (
	FlagNetworkPending Flag = iota
	FlagUnloadBlocker
	FlagExclusiveResource
	FlagUncacheable
	numFlags
)

func (f Flag) valid() bool { return f >= 0 && f < numFlags }

func (f Flag) String() string {
	switch f {
	case FlagNetworkPending:
		return "network-pending"
	case FlagUnloadBlocker:
		return "unload-blocker"
	case FlagExclusiveResource:
		return "exclusive-resource"
	case FlagUncacheable:
		return "uncacheable"
	default:
		return "unknown"
	}
}

// Document is a loaded page. Eligibility is judged only from its flags:
// a document may enter or stay in the cache only when no flag is set.
type Document struct {
	ID       uint64
	flags    [numFlags]bool
	status   DocStatus
	cachedAt time.Time
	order    uint64 // cache insertion sequence, tie-break for equal cachedAt
	heap     int    // index inside the cache heap, -1 when not cached
}

func newDocument(id uint64) *Document {
	return &Document{ID: id, status: DocActive, heap: -1}
}

// eligible reports whether the document may be cached, and why not.
func (d *Document) eligible() (bool, string) {
	for f := Flag(0); f < numFlags; f++ {
		if d.flags[f] {
			return false, f.String()
		}
	}
	return true, "eligible"
}
