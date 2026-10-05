// Package review keeps review verdicts and status check results for a
// single merge request.
package review

// Verdict is a review decision. VerdictComment is never stored and
// never alters the reviewer's previously recorded verdict.
type Verdict int

const (
	VerdictNone Verdict = iota
	VerdictApprove
	VerdictRequestChanges
	VerdictComment
)

// Valid reports whether v is a submittable verdict.
func (v Verdict) Valid() bool {
	return v == VerdictApprove || v == VerdictRequestChanges || v == VerdictComment
}

// Ledger stores the latest non-comment verdict of each reviewer.
type Ledger struct {
	verdicts map[string]Verdict
}

// NewLedger returns an empty verdict ledger.
func NewLedger() *Ledger {
	return &Ledger{verdicts: make(map[string]Verdict)}
}

// Submit records a non-comment verdict; comments leave the stored
// verdict untouched.
func (l *Ledger) Submit(user string, v Verdict) {
	if v == VerdictApprove || v == VerdictRequestChanges {
		l.verdicts[user] = v
	}
}

// Dismiss removes the reviewer's verdict and reports whether one was
// recorded.
func (l *Ledger) Dismiss(user string) bool {
	if _, ok := l.verdicts[user]; !ok {
		return false
	}
	delete(l.verdicts, user)
	return true
}

// ClearApprovals drops approve verdicts only; change requests survive.
func (l *Ledger) ClearApprovals() {
	for user, v := range l.verdicts {
		if v == VerdictApprove {
			delete(l.verdicts, user)
		}
	}
}

// Entries returns a snapshot of all recorded verdicts.
func (l *Ledger) Entries() map[string]Verdict {
	out := make(map[string]Verdict, len(l.verdicts))
	for user, v := range l.verdicts {
		out[user] = v
	}
	return out
}

// Status is the result of a status check on a given head.
type Status int

const (
	StatusPending Status = iota
	StatusSuccess
	StatusFailure
	StatusNeutral
	StatusSkipped
)

// Valid reports whether s is a reportable status.
func (s Status) Valid() bool {
	return s >= StatusPending && s <= StatusSkipped
}

// Passes reports whether s satisfies a required check.
func (s Status) Passes() bool {
	return s == StatusSuccess || s == StatusNeutral || s == StatusSkipped
}

type checkKey struct {
	check string
	head  int
}

// Checks archives check results keyed by (check, head); the last
// report for a key wins.
type Checks struct {
	results map[checkKey]Status
}

// NewChecks returns an empty check ledger.
func NewChecks() *Checks {
	return &Checks{results: make(map[checkKey]Status)}
}

// Report archives the result of check at head.
func (c *Checks) Report(check string, head int, s Status) {
	c.results[checkKey{check: check, head: head}] = s
}

// At returns the archived result of check at head.
func (c *Checks) At(check string, head int) (Status, bool) {
	s, ok := c.results[checkKey{check: check, head: head}]
	return s, ok
}
