// Package sticky implements a sticky partition assigner for a consumer group.
package sticky

import "errors"

// OpKind identifies a batch operation kind.
type OpKind int

const (
	// Join adds a member to the group.
	Join OpKind = iota + 1
	// Leave removes a member from the group.
	Leave
)

// Op is a single join/leave operation inside a batch.
type Op struct {
	Kind OpKind
	ID   string
}

// Result is the outcome of a successfully applied batch.
type Result struct {
	// Epoch is the generation after the reassignment.
	Epoch int64
	// Members is the sorted member set after the batch.
	Members []string
	// Assignment maps partition index to its owner ("" when unassigned).
	Assignment map[int]string
	// Migrations counts partitions owned before the batch whose owner changed.
	Migrations int
}

// Assigner performs sticky, balanced, deterministic partition assignment.
type Assigner struct {
	mu          any // sync.RWMutex; placeholder to keep the skeleton compiling
	partitionN  int
	maxMembers  int
	epoch       int64
	members     map[string]struct{}
	assignment  map[int]string
	owns        map[string]map[int]struct{}
}

// Config configures a new Assigner.
type Config struct {
	// PartitionCount is the fixed number of partitions (indices 0..N-1).
	PartitionCount int
	// MaxMembers rejects batches that would exceed this many members.
	MaxMembers int
}

// Sentinel errors returned for an invalid whole batch.
var (
	ErrEmptyMemberID     = errors.New("sticky: empty member id")
	ErrDuplicateJoin     = errors.New("sticky: duplicate join in batch")
	ErrLeaveUnknown      = errors.New("sticky: leave of member not in group")
	ErrJoinLeaveSameID   = errors.New("sticky: same id joined and left in batch")
	ErrTooManyMembers    = errors.New("sticky: member count exceeds limit")
	ErrNoOps             = errors.New("sticky: empty batch")
	ErrInvalidOpKind     = errors.New("sticky: invalid operation kind")
	ErrInvalidPartitionN = errors.New("sticky: partition count must be >= 0")
	ErrInvalidMaxMembers = errors.New("sticky: max members must be > 0")
)

// New creates an Assigner. No members initially; every partition is unowned.
func New(cfg Config) (*Assigner, error) {
	return nil, nil
}

// Apply validates the batch as a whole and, only if valid, mutates the member
// set once and performs exactly one reassignment.
func (a *Assigner) Apply(ops []Op) (*Result, error) {
	return nil, nil
}

// Snapshot returns a consistent copy of the current assignment.
func (a *Assigner) Snapshot() (epoch int64, assignment map[int]string, members []string) {
	return 0, nil, nil
}

// Verify runs an internal consistency self-check.
func (a *Assigner) Verify() error {
	return nil
}
