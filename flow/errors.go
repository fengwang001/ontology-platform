package flow

// Sentinel errors. Each invalid-input category maps to a distinct error so
// callers can distinguish rejection reasons with errors.Is.
var (
	// ErrInvalidParam means a non-positive parameter was supplied.
	ErrInvalidParam = flowError("invalid non-positive parameter")
	// ErrBacklogOverflow means producing the messages would exceed the
	// configured backlog limit.
	ErrBacklogOverflow = flowError("backlog limit exceeded")
	// ErrConsumeOutOfRange means the requested consume count is not within
	// the contiguous range of produced-but-not-yet-consumed sequence numbers.
	ErrConsumeOutOfRange = flowError("consume sequence out of range")
	// ErrProbeNotAllowed means a probe is only legal while credit is zero and
	// the backlog is non-empty.
	ErrProbeNotAllowed = flowError("probe not allowed: requires zero credit and non-empty backlog")
)

type flowError string

func (e flowError) Error() string { return string(e) }
