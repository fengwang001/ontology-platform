package sticky

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// OpKind identifies a batch operation type.
type OpKind int

const (
	OpJoin OpKind = iota
	OpLeave
)

// Op is a single join or leave operation inside a batch.
type Op struct {
	Kind OpKind
	ID   string
}

// Join returns a join operation.
func Join(id string) Op { return Op{Kind: OpJoin, ID: id} }

// Leave returns a leave operation.
func Leave(id string) Op { return Op{Kind: OpLeave, ID: id} }

// Assignor performs sticky partition assignment for a consumer group.
type Assignor struct {
	mu sync.RWMutex

	maxMembers int
	partitions int

	generation uint64
	assignment map[int]string
	members    map[string]struct{}

	logw io.Writer
}

// New creates an Assignor for the given partition count and member limit.
// An empty logw disables logging.
func New(partitionCount, maxMembers int, logw io.Writer) (*Assignor, error) {
	if partitionCount <= 0 {
		return nil, fmt.Errorf("%w: partitionCount must be positive, got %d", ErrInvalidArgument, partitionCount)
	}
	if maxMembers <= 0 {
		return nil, fmt.Errorf("%w: maxMembers must be positive, got %d", ErrInvalidArgument, maxMembers)
	}
	return &Assignor{
		maxMembers: maxMembers,
		partitions: partitionCount,
		generation: 0,
		assignment: make(map[int]string),
		members:    make(map[string]struct{}),
		logw:       logw,
	}, nil
}

// Batch validates the batch atomically and, on success, applies the whole
// membership change followed by exactly one rebalance. A rejected batch never
// changes assignment or generation.
func (a *Assignor) Batch(ops []Op) error {
	if len(ops) == 0 {
		a.logf("batch: empty batch, accepted as no-op, generation=%d unchanged", a.generation)
		return nil
	}

	// First-pass validation happens before any state is touched, so a rejected
	// batch leaves no trace.
	joinSet := make(map[string]int)
	desired := make(map[string]struct{})

	for idx, op := range ops {
		switch op.Kind {
		case OpJoin, OpLeave:
		default:
			a.logf("batch: op[%d] has invalid kind %d, rejected (%v)", idx, op.Kind, ErrInvalidArgument)
			return fmt.Errorf("%w: op[%d]: invalid op kind %d", ErrInvalidArgument, idx, op.Kind)
		}
		if op.ID == "" {
			a.logf("batch: op[%d] %s has empty member id, rejected (%v)", idx, opName(op.Kind), ErrEmptyMemberID)
			return fmt.Errorf("%w: op[%d] %s", ErrEmptyMemberID, idx, opName(op.Kind))
		}
		switch op.Kind {
		case OpJoin:
			if first, dup := joinSet[op.ID]; dup {
				a.logf("batch: duplicate join of %q at op[%d] and op[%d], rejected (%v)", op.ID, first, idx, ErrDuplicateJoin)
				return fmt.Errorf("%w: op[%d]: %q already joined at op[%d]", ErrDuplicateJoin, idx, op.ID, first)
			}
			joinSet[op.ID] = idx
			desired[op.ID] = struct{}{}
		case OpLeave:
			if _, joinsInBatch := joinSet[op.ID]; !joinsInBatch {
				a.mu.RLock()
				_, present := a.members[op.ID]
				a.mu.RUnlock()
				if !present {
					a.logf("batch: op[%d] leaves unknown member %q, rejected (%v)", idx, op.ID, ErrMemberNotFound)
					return fmt.Errorf("%w: op[%d]: %q", ErrMemberNotFound, idx, op.ID)
				}
			}
			delete(desired, op.ID)
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Recompute under the write lock from authoritative state; the desired
	// set is an unordered set operation, so intra-batch order never matters.
	desired = make(map[string]struct{}, len(a.members)+len(joinSet))
	for id := range a.members {
		desired[id] = struct{}{}
	}
	for id := range joinSet {
		desired[id] = struct{}{}
	}
	for _, op := range ops {
		if op.Kind == OpLeave {
			delete(desired, op.ID)
		}
	}
	if len(desired) > a.maxMembers {
		a.logf("batch: resulting member count %d exceeds limit %d, rejected (%v)", len(desired), a.maxMembers, ErrTooManyMembers)
		return fmt.Errorf("%w: resulting count %d > limit %d", ErrTooManyMembers, len(desired), a.maxMembers)
	}

	beforeMembers := sortedKeys(a.members)
	beforeAssign := copyAssignment(a.assignment, a.partitions)

	a.members = desired
	afterMembers := sortedKeys(desired)

	a.logf("batch: ops=%s applied, rebalance with members %v", formatOps(ops), afterMembers)
	a.assignment, a.generation = a.rebalanceLocked(beforeMembers, beforeAssign, afterMembers)

	a.logf("batch: committed generation=%d members=%v assignment=%s",
		a.generation, afterMembers, formatAssignment(a.assignment, a.partitions))
	return nil
}

// rebalanceLocked computes the new assignment from pre-batch ownership.
//
// Quota: every member gets base = P/M; the P%M remainder slots go to members
// ordered by pre-batch holding count descending, then id ascending.
// Release: holders over quota shed partitions by partition number descending.
// Fill: ownerless partitions are assigned by partition number ascending, each
// to the member with the largest remaining quota gap (smallest id on tie).
func (a *Assignor) rebalanceLocked(beforeMembers []string, before map[int]string, afterMembers []string) (map[int]string, uint64) {
	m := len(afterMembers)
	assignment := make(map[int]string, a.partitions)

	if m == 0 {
		migrations := countMigrations(before, assignment, a.partitions)
		a.logf("rebalance gen %d->%d: no members, released all partitions, migrations=%d",
			a.generation, a.generation+1, migrations)
		return assignment, a.generation + 1
	}

	heldBefore := make(map[string]int, len(beforeMembers))
	for _, id := range beforeMembers {
		heldBefore[id] = 0
	}
	for p := 0; p < a.partitions; p++ {
		if owner, ok := before[p]; ok {
			heldBefore[owner]++
		}
	}

	base := a.partitions / m
	remainder := a.partitions % m

	order := make([]string, len(afterMembers))
	copy(order, afterMembers)
	sort.SliceStable(order, func(i, j int) bool {
		if heldBefore[order[i]] != heldBefore[order[j]] {
			return heldBefore[order[i]] > heldBefore[order[j]]
		}
		return order[i] < order[j]
	})

	quota := make(map[string]int, m)
	for _, id := range afterMembers {
		quota[id] = base
	}
	for i := 0; i < remainder; i++ {
		quota[order[i]]++
	}
	a.logf("rebalance gen %d->%d: base=%d remainder=%d quotaOrder=%v quota=%s",
		a.generation, a.generation+1, base, remainder, order, quotaMap(quota, order))

	current := make(map[string]int, m)

	// Release phase: an over-quota member sheds its highest-numbered
	// partitions, i.e. scanning ascending it keeps the first quota-owned
	// partitions and drops the rest. Partitions whose pre-batch owner is gone
	// are ownerless immediately.
	for p := 0; p < a.partitions; p++ {
		owner, ok := before[p]
		if !ok {
			continue
		}
		if _, stillMember := quota[owner]; !stillMember {
			continue
		}
		if current[owner] < quota[owner] {
			assignment[p] = owner
			current[owner]++
		} else {
			a.logf("rebalance: partition %d released by %s (over quota %d)", p, owner, quota[owner])
		}
	}

	// Fill phase: ownerless partitions ascending; largest gap wins, smallest
	// id breaks the tie.
	for p := 0; p < a.partitions; p++ {
		if _, owned := assignment[p]; owned {
			continue
		}
		winner := ""
		winnerGap := -1
		for _, id := range afterMembers {
			gap := quota[id] - current[id]
			if gap > winnerGap || (gap == winnerGap && (winner == "" || id < winner)) {
				winner = id
				winnerGap = gap
			}
		}
		old, hadOwner := before[p]
		a.logf("rebalance: partition %d unowned (old=%s) -> %s, gap=%d",
			p, oldOrNone(old, hadOwner), winner, winnerGap)
		assignment[p] = winner
		current[winner]++
	}

	migrations := countMigrations(before, assignment, a.partitions)
	a.logf("rebalance: assignment=%s held=%s migrations=%d",
		formatAssignment(assignment, a.partitions), heldMap(current, afterMembers), migrations)
	return assignment, a.generation + 1
}

// Snapshot is an immutable view of one assignment state.
type Snapshot struct {
	Generation uint64
	Members    []string
	Assignment map[int]string
}

// Query returns the current generation and a copy of the assignment.
func (a *Assignor) Query() Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()

	members := sortedKeys(a.members)
	assignment := make(map[int]string, len(a.assignment))
	for p, owner := range a.assignment {
		assignment[p] = owner
	}
	a.logf("query: generation=%d members=%v assignment=%s",
		a.generation, members, formatAssignment(assignment, a.partitions))
	return Snapshot{
		Generation: a.generation,
		Members:    members,
		Assignment: assignment,
	}
}

// Verify runs self-consistency checks on the current assignment.
func (a *Assignor) Verify() error {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if len(a.assignment) > a.partitions {
		return fmt.Errorf("%w: assigned partition count %d exceeds %d", ErrInvalidArgument, len(a.assignment), a.partitions)
	}
	held := make(map[string]int, len(a.members))
	for id := range a.members {
		held[id] = 0
	}
	for p := 0; p < a.partitions; p++ {
		owner, ok := a.assignment[p]
		if len(a.members) == 0 {
			if ok {
				return fmt.Errorf("%w: partition %d assigned to %q with no members", ErrInvalidArgument, p, owner)
			}
			continue
		}
		if !ok {
			return fmt.Errorf("%w: partition %d has no owner", ErrInvalidArgument, p)
		}
		if _, member := a.members[owner]; !member {
			return fmt.Errorf("%w: partition %d owned by non-member %q", ErrInvalidArgument, p, owner)
		}
		held[owner]++
	}
	min, max := -1, -1
	for _, n := range held {
		if min == -1 || n < min {
			min = n
		}
		if n > max {
			max = n
		}
	}
	if len(a.members) > 0 && max-min > 1 {
		return fmt.Errorf("%w: unbalanced holdings min=%d max=%d", ErrInvalidArgument, min, max)
	}
	a.logf("verify: generation=%d ok, held=%s", a.generation, heldMap(held, sortedKeys(a.members)))
	return nil
}

func (a *Assignor) logf(format string, args ...any) {
	if a.logw != nil {
		fmt.Fprintf(a.logw, format+"\n", args...)
	}
}

func countMigrations(before, after map[int]string, partitions int) int {
	n := 0
	for p := 0; p < partitions; p++ {
		old, hadOwner := before[p]
		newOwner, hasOwner := after[p]
		if hadOwner && hasOwner && old != newOwner {
			n++
		}
	}
	return n
}

func copyAssignment(src map[int]string, partitions int) map[int]string {
	dst := make(map[int]string, partitions)
	for p, owner := range src {
		dst[p] = owner
	}
	return dst
}

func sortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func opName(k OpKind) string {
	switch k {
	case OpJoin:
		return "join"
	case OpLeave:
		return "leave"
	default:
		return "invalid"
	}
}

func formatOps(ops []Op) string {
	parts := make([]string, 0, len(ops))
	for _, op := range ops {
		parts = append(parts, opName(op.Kind)+":"+op.ID)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func formatAssignment(m map[int]string, partitions int) string {
	parts := make([]string, 0, partitions)
	for p := 0; p < partitions; p++ {
		if owner, ok := m[p]; ok {
			parts = append(parts, fmt.Sprintf("%d:%s", p, owner))
		} else {
			parts = append(parts, fmt.Sprintf("%d:-", p))
		}
	}
	return "{" + strings.Join(parts, " ") + "}"
}

func quotaMap(quota map[string]int, order []string) string {
	parts := make([]string, 0, len(order))
	for _, id := range order {
		parts = append(parts, fmt.Sprintf("%s=%d", id, quota[id]))
	}
	return "{" + strings.Join(parts, " ") + "}"
}

func heldMap(held map[string]int, order []string) string {
	parts := make([]string, 0, len(order))
	for _, id := range order {
		parts = append(parts, fmt.Sprintf("%s=%d", id, held[id]))
	}
	return "{" + strings.Join(parts, " ") + "}"
}

func oldOrNone(old string, hadOwner bool) string {
	if hadOwner {
		return old
	}
	return "-"
}
