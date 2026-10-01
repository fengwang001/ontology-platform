package quorum

import (
	"log"
	"os"
	"sort"
	"sync"
)

// Logger is the logging sink used to record every operation's input, output
// and the reason behind its quorum decision.
type Logger interface {
	Printf(format string, args ...any)
}

// Arbiter decides log-commit quorum across single and joint configurations.
//
// All methods are safe for concurrent use; their effects are equivalent to
// some serial ordering. Replaying the same serial operation sequence yields
// exactly the same commit indices and errors.
type Arbiter struct {
	mu sync.Mutex

	joint bool

	// Active voting set of a single configuration, or the old set while joint.
	old []string
	// New voting set; populated only while joint.
	new []string

	// Match index of every known node (including nodes outside the active
	// configuration, e.g. newly added voters with match index 0).
	match map[string]uint64

	cfgIdx   uint64 // index of the committed single-configuration entry
	jointIdx uint64 // index of the active joint-configuration entry
	commit   uint64

	log Logger
}

// New constructs an Arbiter whose initial single configuration is nodes.
// nodes must be non-empty and contain no duplicates or empty identifiers.
func New(nodes []string) (*Arbiter, error) {
	if len(nodes) == 0 {
		return nil, ErrEmptyNodes
	}
	if err := validateSet(nodes); err != nil {
		return nil, err
	}

	set := append([]string(nil), nodes...)
	sort.Strings(set)

	match := make(map[string]uint64, len(set))
	for _, node := range set {
		match[node] = 0
	}

	return &Arbiter{
		old:   set,
		match: match,
		log:   log.New(os.Stdout, "[quorum] ", log.LstdFlags|log.Lmicroseconds),
	}, nil
}

// SetLogger replaces the operation logger (nil disables logging).
func (a *Arbiter) SetLogger(l Logger) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.log = l
}

// Ack records node's match index as max(previous, idx) and recomputes commit.
// An ack whose idx is below the current match index is not an error. Acks from
// unknown (forgotten or never-seen) nodes are rejected without state change.
func (a *Arbiter) Ack(node string, idx uint64) (uint64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	prev, ok := a.match[node]
	if !ok {
		a.recordf("Ack(%q,%d) -> error: %s", node, idx, ErrUnknownNode)
		return a.commit, ErrUnknownNode
	}

	if idx > prev {
		a.match[node] = idx
	}
	before := a.commit
	a.recompute()
	a.recordf("Ack(%q,%d): match %d->%d, commit %d->%d (%s)",
		node, idx, prev, a.match[node], before, a.commit, a.quorumDesc())
	return a.commit, nil
}

// BeginJoint transitions immediately from a single configuration to the joint
// configuration (old, newSet); every later quorum decision uses both majorities
// even before the joint entry is committed. Previously unknown members of
// newSet become known with match index 0.
//
// Validation order: single configuration, newSet non-empty, newSet valid
// (duplicates/empty identifiers), newSet differs from the current set,
// idx > cfgIdx. Only the first failing check is reported.
func (a *Arbiter) BeginJoint(newSet []string, idx uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.joint {
		a.recordf("BeginJoint(%v,%d) -> error: %s", newSet, idx, ErrNotSingle)
		return ErrNotSingle
	}
	if len(newSet) == 0 {
		a.recordf("BeginJoint(%v,%d) -> error: %s", newSet, idx, ErrEmptySet)
		return ErrEmptySet
	}
	if err := validateSet(newSet); err != nil {
		a.recordf("BeginJoint(%v,%d) -> error: %s", newSet, idx, err)
		return err
	}
	if sameSet(a.old, newSet) {
		a.recordf("BeginJoint(%v,%d) -> error: %s", newSet, idx, ErrSameSet)
		return ErrSameSet
	}
	if idx <= a.cfgIdx {
		a.recordf("BeginJoint(%v,%d) -> error: %s (cfgIdx=%d)",
			newSet, idx, ErrIndexNotAfterCfg, a.cfgIdx)
		return ErrIndexNotAfterCfg
	}

	set := append([]string(nil), newSet...)
	sort.Strings(set)

	for _, node := range set {
		if _, ok := a.match[node]; !ok {
			a.match[node] = 0
		}
	}

	a.joint = true
	a.jointIdx = idx
	a.new = set

	before := a.commit
	a.recompute()
	a.recordf("BeginJoint(%v,%d): joint (old=%v,new=%v), commit %d->%d (%s)",
		set, idx, a.old, a.new, before, a.commit, a.quorumDesc())
	return nil
}

// FinishJoint transitions immediately to the single configuration newSet and
// forgets every node outside it. It requires the joint entry to be committed.
//
// Validation order: joint configuration, idx > jointIdx, commit >= jointIdx.
// Only the first failing check is reported.
func (a *Arbiter) FinishJoint(idx uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.joint {
		a.recordf("FinishJoint(%d) -> error: %s", idx, ErrNotJoint)
		return ErrNotJoint
	}
	if idx <= a.jointIdx {
		a.recordf("FinishJoint(%d) -> error: %s (jointIdx=%d)",
			idx, ErrIndexNotAfterJoint, a.jointIdx)
		return ErrIndexNotAfterJoint
	}
	if a.commit < a.jointIdx {
		a.recordf("FinishJoint(%d) -> error: %s (commit=%d, jointIdx=%d)",
			idx, ErrJointNotCommitted, a.commit, a.jointIdx)
		return ErrJointNotCommitted
	}

	newSet := a.new
	for node := range a.match {
		if _, found := sort.Find(len(newSet), func(i int) int {
			switch {
			case node < newSet[i]:
				return -1
			case node > newSet[i]:
				return 1
			default:
				return 0
			}
		}); !found {
			delete(a.match, node)
		}
	}

	a.joint = false
	a.old = newSet
	a.new = nil
	a.cfgIdx = idx

	before := a.commit
	a.recompute()
	a.recordf("FinishJoint(%d): single=%v, cfgIdx=%d, commit %d->%d (%s)",
		idx, a.old, a.cfgIdx, before, a.commit, a.quorumDesc())
	return nil
}

// Commit returns the current commit index.
func (a *Arbiter) Commit() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.commit
}

// CfgIdx returns the index of the committed single-configuration entry.
func (a *Arbiter) CfgIdx() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfgIdx
}

// Joint reports whether the arbiter is in a joint configuration.
func (a *Arbiter) Joint() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.joint
}

// Match returns the match index of a known node and whether it is known.
func (a *Arbiter) Match(node string) (uint64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	idx, ok := a.match[node]
	return idx, ok
}

// recompute advances commit to the greatest N committable under the active
// configuration; commit never decreases. Caller holds mu.
func (a *Arbiter) recompute() {
	n := maxCommittable(a.old, a.match)
	if a.joint {
		if m := maxCommittable(a.new, a.match); m < n {
			n = m
		}
	}
	if n > a.commit {
		a.commit = n
	}
}

// maxCommittable returns the greatest index replicated to a strict majority
// (floor(size/2)+1 members) of the given voting set.
func maxCommittable(voters []string, match map[string]uint64) uint64 {
	need := len(voters)/2 + 1
	replicated := make([]uint64, 0, len(voters))
	for _, node := range voters {
		replicated = append(replicated, match[node])
	}
	sort.Sort(sort.Reverse(uintSlice(replicated)))
	return replicated[need-1]
}

type uintSlice []uint64

func (s uintSlice) Len() int           { return len(s) }
func (s uintSlice) Less(i, j int) bool { return s[i] < s[j] }
func (s uintSlice) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

func (a *Arbiter) quorumDesc() string {
	if a.joint {
		return "joint: majority of old AND majority of new required"
	}
	return "single: majority of the active set required"
}

func (a *Arbiter) recordf(format string, args ...any) {
	if a.log != nil {
		a.log.Printf(format, args...)
	}
}

// validateSet checks non-emptiness is handled by callers where required;
// here it rejects empty identifiers and duplicates.
func validateSet(nodes []string) error {
	seen := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if node == "" {
			return ErrEmptyNodeID
		}
		if _, dup := seen[node]; dup {
			return ErrDuplicateNode
		}
		seen[node] = struct{}{}
	}
	return nil
}

// sameSet compares order-insensitively; both arguments are the same length
// when relevant (caller guarantees newSet non-empty; old is always non-empty).
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
