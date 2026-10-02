package scheduler

import "fmt"

// Reason identifies why an operation was rejected. Every rejection carries
// exactly one distinguishable reason.
type Reason string

const (
	// ReasonClockBackwards: the call time is earlier than any previously
	// seen time. Checked before all other reasons.
	ReasonClockBackwards Reason = "clock_backwards"
	// ReasonInvalidPeriod: the decay period is not a positive integer.
	ReasonInvalidPeriod Reason = "invalid_period"
	// ReasonInvalidShare: the account share is not a positive integer.
	ReasonInvalidShare Reason = "invalid_share"
	// ReasonDuplicateAccount: the account is already registered.
	ReasonDuplicateAccount Reason = "duplicate_account"
	// ReasonAccountNotRegistered: the target account does not exist.
	ReasonAccountNotRegistered Reason = "account_not_registered"
	// ReasonInvalidCost: the job cost is not a positive integer.
	ReasonInvalidCost Reason = "invalid_cost"
	// ReasonDuplicateJobID: the job ID was already submitted (even if the
	// job has since been dispatched).
	ReasonDuplicateJobID Reason = "duplicate_job_id"
	// ReasonNoJobs: dispatch was requested while every queue is empty.
	ReasonNoJobs Reason = "no_jobs"
)

// Error is a rejection with a machine-distinguishable reason.
type Error struct {
	Reason Reason
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Reason, e.Detail)
}

func reject(reason Reason, format string, args ...any) *Error {
	return &Error{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}
