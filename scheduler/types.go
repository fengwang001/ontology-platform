package scheduler

import "math/big"

// Candidate describes one account that took part in a dispatch decision.
type Candidate struct {
	AccountID string
	Usage     *big.Int
	Share     int64
}

// DispatchResult reports the dispatched job and the full basis of the
// decision, so the choice can be audited and replayed.
type DispatchResult struct {
	Job Job
	// Winner is the account whose head job was dispatched.
	Winner string
	// Candidates lists every account with a non-empty queue, with its
	// usage and share at decision time (after decay, before charging).
	Candidates []Candidate
	// Reason explains why the winner was selected.
	Reason string
}

// AccountSnapshot is a point-in-time view of one account.
type AccountSnapshot struct {
	ID       string
	Share    int64
	Usage    *big.Int
	QueueLen int
	Queued   []string
}

// Snapshot is a consistent view of the whole scheduler.
type Snapshot struct {
	Time              int64
	BoundariesApplied int64
	Accounts          []AccountSnapshot
}
