package approval

import (
	"errors"
	"sync"

	"ontology/deadline"
	"ontology/org"
)

var (
	ErrInvalid     = errors.New("approval: invalid argument")
	ErrClock       = errors.New("approval: now before current clock")
	ErrExist       = errors.New("approval: request already exists")
	ErrNotFound    = errors.New("approval: request not found")
	ErrClosed      = errors.New("approval: request already finalized")
	ErrNotAssignee = errors.New("approval: who is not the current assignee")
	ErrRevoked     = errors.New("approval: approver limit is below amount")
	ErrNoApprover  = errors.New("approval: no eligible approver in chain")
)

type Outcome int

const (
	Pending Outcome = iota
	Approved
	Rejected
	Expired
)

type Status struct {
	Outcome    Outcome
	Assignee   string
	AssignedAt int64
	FinalAt    int64
}

type request struct {
	amount     int64
	candidates []string // frozen at submit time, chain order
	pos        int
	ta         int64 // assignment time of the current candidate
	outcome    Outcome
	finalAt    int64
}

// Engine runs the approval workflow over one organization.
// All operations are safe for concurrent use and are linearizable.
type Engine struct {
	mu       sync.RWMutex
	org      *org.Org
	heap     *deadline.Heap
	t        int64
	clock    int64
	reqs     map[string]*request
	examined int64 // heap items examined during the latest successful mutating op
}

const maxAmount = 1_000_000_000_000
const maxNow = 100_000_000_000_000

// New creates an engine. T is the per-level timeout, 1..1e9; an
// out-of-range T yields an error wrapping ErrInvalid.
func New(o *org.Org, T int64) (*Engine, error) {
	if T < 1 || T > 1_000_000_000 {
		return nil, ErrInvalid
	}
	return &Engine{
		org:  o,
		heap: deadline.New(),
		t:    T,
		reqs: make(map[string]*request),
	}, nil
}
