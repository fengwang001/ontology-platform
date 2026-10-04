// Package rule maps a job's when-condition and its upstream outcome
// flags to the state the job must enter. It is a pure function package:
// same inputs always yield the same decision, which is what makes
// pipeline runs exactly replayable.
package rule

// When is a job's trigger condition.
type When int

const (
	OnSuccess When = iota
	OnFailure
	Always
	Manual
)

// ParseWhen converts the spec string to a When.
func ParseWhen(s string) (When, bool) {
	switch s {
	case "on_success":
		return OnSuccess, true
	case "on_failure":
		return OnFailure, true
	case "always":
		return Always, true
	case "manual":
		return Manual, true
	}
	return 0, false
}

func (w When) String() string {
	switch w {
	case OnSuccess:
		return "on_success"
	case OnFailure:
		return "on_failure"
	case Always:
		return "always"
	case Manual:
		return "manual"
	}
	return "unknown"
}

// Decision is the state a Created job enters once all its needs are
// decided.
type Decision int

const (
	ToPending Decision = iota
	ToManual
	ToSkipped
)

// Decide evaluates a when-condition. bad reports that some need Failed
// without allowFailure; skip reports that some need is Skipped or
// Canceled. A need in a non-blocking manual gate (Manual with
// allowFailure) counts as Success and sets neither flag.
func Decide(w When, bad, skip bool) Decision {
	switch w {
	case OnSuccess:
		if bad || skip {
			return ToSkipped
		}
		return ToPending
	case OnFailure:
		// Skipped upstream is not a failure; with no needs at all
		// (bad == false) the job is Skipped as well.
		if bad {
			return ToPending
		}
		return ToSkipped
	case Always:
		return ToPending
	case Manual:
		if bad || skip {
			return ToSkipped
		}
		return ToManual
	}
	return ToSkipped
}
