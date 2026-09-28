package asyncbuf

// RejectCause identifies why a call was rejected.
type RejectCause string

const (
	CauseCapacityFull     RejectCause = "capacity_full"
	CauseUnknownID        RejectCause = "unknown_id"
	CauseAlreadyCompleted RejectCause = "already_completed"
	CauseDuplicateID      RejectCause = "duplicate_id"
	CauseEmptyID          RejectCause = "empty_id"
	CauseWatermarkNotInc  RejectCause = "watermark_not_strictly_increasing"
	CauseClosed           RejectCause = "closed"
	CauseInvalidArgument  RejectCause = "invalid_argument"
)

// RejectError carries a machine-readable, pairwise-distinct cause.
type RejectError struct {
	Cause   RejectCause
	Message string
}

func (e *RejectError) Error() string { return string(e.Cause) + ": " + e.Message }
