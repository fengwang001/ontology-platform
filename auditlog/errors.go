package auditlog

import "errors"

// Config and operation errors. They are all distinguishable and carry the
// first rejection reason only; a rejected operation never mutates state.
var (
	// ErrInvalidConfig is returned by New for an illegal k0/cap/maxData
	// combination or missing injected functions.
	ErrInvalidConfig = errors.New("auditlog: invalid configuration")
	// ErrInvalidArg is returned for an out-of-range timestamp or oversized
	// data. It is the first check of every write.
	ErrInvalidArg = errors.New("auditlog: invalid argument")
	// ErrSealed is returned when a write or Export is attempted after Seal.
	ErrSealed = errors.New("auditlog: log is sealed")
	// ErrTimeRollback is returned when ts is smaller than the last accepted
	// timestamp (equal timestamps are allowed).
	ErrTimeRollback = errors.New("auditlog: timestamp rollback")
	// ErrCapFull is returned when Append/Rekey would make the total number
	// of records exceed cap-1 (one slot is reserved for the seal record).
	ErrCapFull = errors.New("auditlog: capacity exhausted")
)

// Verification failure categories. KindInvalidArg covers an illegal
// checkpoint set; every other kind is a distinct, position-bearing failure.
var (
	KindOK                 = ""
	KindInvalidArg         = "invalid_argument"
	KindGap                = "gap"
	KindReplay             = "replay"
	KindAppendAfterSeal    = "append_after_seal"
	KindTimeRollback       = "time_rollback"
	KindTampered           = "tampered"
	KindContentMismatch    = "content_mismatch"
	KindCheckpointConflict = "checkpoint_conflict"
	KindTruncated          = "truncated"
)
