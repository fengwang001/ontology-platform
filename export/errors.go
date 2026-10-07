package export

import "fmt"

// Kind classifies every error raised while starting or running an export cycle.
type Kind int

const (
	// KindStartMismatch: the declared start is not equal to the last
	// confirmed end. This is checked first because accepting an arbitrary
	// start would silently skip or duplicate a prefix of the write log;
	// every later decision depends on having a trustworthy interval.
	KindStartMismatch Kind = iota + 1
	// KindCheckpointUnreadable: the last confirmed end cannot be read or is
	// ambiguous, so a safe start must be re-derived from history. Reachable
	// only when no authoritative checkpoint exists to mismatch against.
	KindCheckpointUnreadable
	// KindHistoryGap: while re-deriving a safe start, the history records
	// themselves have a hole, so no start can be proven gap-free. Checked
	// only on the re-derivation path that an unreadable checkpoint forces.
	KindHistoryGap
	// KindResourceExhausted: output storage/bandwidth gave out mid-cycle;
	// the end is deliberately left unconfirmed so the next cycle replays.
	KindResourceExhausted
)

func (k Kind) String() string {
	switch k {
	case KindStartMismatch:
		return "StartMismatch"
	case KindCheckpointUnreadable:
		return "CheckpointUnreadable"
	case KindHistoryGap:
		return "HistoryGap"
	case KindResourceExhausted:
		return "ResourceExhausted"
	default:
		return fmt.Sprintf("Kind(%d)", int(k))
	}
}

// Error is a classified component error. Classify inspects its Kind instead
// of matching message text, which keeps the fixed priority machine-checkable.
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("export: %s: %s", e.Kind, e.Msg)
}

func mkErr(kind Kind, format string, args ...any) error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// classifyPriority is the single, fixed arbitration order. Start mismatch is
// a logical lie about the interval and must win over environmental failures;
// an unreadable checkpoint must be known before history is consulted; a gap
// is a property of that derivation; resource exhaustion can only occur once a
// valid interval has begun outputting.
var classifyPriority = []Kind{
	KindStartMismatch,
	KindCheckpointUnreadable,
	KindHistoryGap,
	KindResourceExhausted,
}

// Classify returns the fixed-priority Kind among the supplied errors. It is
// used by callers when several conditions surface at once, e.g. a cycle that
// runs out of resources after discovering a stale start declaration.
func Classify(errs ...error) Kind {
	seen := map[Kind]bool{}
	for _, err := range errs {
		if e, ok := err.(*Error); ok {
			seen[e.Kind] = true
		}
	}
	for _, k := range classifyPriority {
		if seen[k] {
			return k
		}
	}
	return 0
}
