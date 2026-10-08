package ontology

import "sync"

// OpKind identifies the kind of a public operation.
type OpKind int

const (
	OpCreateLink OpKind = iota
	OpDeleteLink
	OpDeleteObject
)

func (k OpKind) String() string {
	switch k {
	case OpCreateLink:
		return "create_link"
	case OpDeleteLink:
		return "delete_link"
	case OpDeleteObject:
		return "delete_object"
	default:
		return "unknown"
	}
}

// LinkRef references one concrete link between two object instances.
type LinkRef struct {
	Type LinkTypeID
	Src  ObjectID
	Dst  ObjectID
}

// SkipReason explains why a link was not cleaned during a cascade.
type SkipReason int

const (
	// SkipInvisible: the initiating subject cannot read the participation
	// property of the instance being cleaned (check mode only).
	SkipInvisible SkipReason = iota
	// SkipPermission: the initiating subject lacks write permission on a
	// participation property at one end of the link (check mode only).
	SkipPermission
	// SkipCardinality: removing the link would push the surviving other
	// end below the declared minimum cardinality.
	SkipCardinality
)

func (r SkipReason) String() string {
	switch r {
	case SkipInvisible:
		return "invisible"
	case SkipPermission:
		return "permission"
	case SkipCardinality:
		return "cardinality"
	default:
		return "unknown"
	}
}

// SkippedLink records a link that cascade cleanup deliberately left in
// place, with a distinguishable reason.
type SkippedLink struct {
	Link   LinkRef
	Reason SkipReason
}

// PermCheckRecord is the logged basis of one permission check.
type PermCheckRecord struct {
	Subject  SubjectID
	Object   ObjectID
	Property PropertyID
	Need     string // "read" or "write"
	Granted  bool
}

// CardCheckRecord is the logged basis of one cardinality evaluation.
type CardCheckRecord struct {
	Object   ObjectID
	LinkType LinkTypeID
	Before   int
	After    int
	Min      int
	Max      int
	OK       bool
}

// LogEntry fully records one public call: its input, its final output and
// the permission/cardinality basis on which the decision was made.
type LogEntry struct {
	Seq        uint64
	Op         OpKind
	Subject    SubjectID
	LinkType   LinkTypeID
	Src        ObjectID
	Dst        ObjectID
	Target     ObjectID
	PermChecks []PermCheckRecord
	CardChecks []CardCheckRecord
	ErrClass   ErrorClass
	Cleaned    []LinkRef
	Skipped    []SkippedLink
	Deleted    []ObjectID
	ClockAfter uint64
}

// Logger receives one entry per public operation, emitted in the serial
// order in which operations were applied.
type Logger interface {
	Log(LogEntry)
}

// MemoryLogger is a thread-safe in-memory Logger used by tests and by the
// demo server.
type MemoryLogger struct {
	mu      sync.Mutex
	entries []LogEntry
}

// Log records one entry.
func (l *MemoryLogger) Log(e LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, e)
}

// Entries returns a copy of all recorded entries in emission order.
func (l *MemoryLogger) Entries() []LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]LogEntry, len(l.entries))
	copy(out, l.entries)
	return out
}
