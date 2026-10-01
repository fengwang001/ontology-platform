package pbftlog

// Sentinel errors returned by Handle and New. Each invalid condition has its
// own distinguishable error; checks follow the strict priority order given in
// the package specification.
var (
	// ErrInvalidFaultBound is returned by New when f < 1.
	ErrInvalidFaultBound = rejectError("pbftlog: f must be >= 1")
	// ErrInvalidLimit is returned by New when L < 1.
	ErrInvalidLimit = rejectError("pbftlog: window limit L must be >= 1")
	// ErrInvalidView is returned by New when the view is negative.
	ErrInvalidView = rejectError("pbftlog: view must be >= 0")

	// ErrSenderOutOfRange: from is not in [0, N).
	ErrSenderOutOfRange = rejectError("pbftlog: sender id out of range")
	// ErrEmptyDigest: digest is the empty string.
	ErrEmptyDigest = rejectError("pbftlog: digest must be non-empty")
	// ErrWrongView: message view differs from the fixed view.
	ErrWrongView = rejectError("pbftlog: message view differs from current view")
	// ErrSeqOutOfWindow: seq <= executed or seq > executed+L.
	ErrSeqOutOfWindow = rejectError("pbftlog: sequence number outside window")
	// ErrPrePrepareFromBackup: a non-primary sent a pre-prepare.
	ErrPrePrepareFromBackup = rejectError("pbftlog: pre-prepare must come from primary")
	// ErrPrepareFromPrimary: the primary sent a prepare.
	ErrPrepareFromPrimary = rejectError("pbftlog: prepare must not come from primary")
	// ErrConflictingPrePrepare: a pre-prepare with a different digest for the
	// same (view, seq) already exists.
	ErrConflictingPrePrepare = rejectError("pbftlog: conflicting pre-prepare digest for (view, seq)")
	// ErrConflictingPrepare: the same sender already prepared a different
	// digest for (view, seq).
	ErrConflictingPrepare = rejectError("pbftlog: conflicting prepare digest from same sender")
	// ErrConflictingCommit: the same sender already committed a different
	// digest for (view, seq).
	ErrConflictingCommit = rejectError("pbftlog: conflicting commit digest from same sender")
)

// RejectError is the concrete type of every rejection sentinel, so callers can
// detect rule violations via errors.As.
type RejectError string

func (e RejectError) Error() string { return string(e) }

func rejectError(s string) RejectError { return RejectError(s) }
