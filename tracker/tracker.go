// Package tracker implements the membership, sequence-number assignment,
// acknowledgement, global-checkpoint and confirmation bookkeeping of a
// primary/replica replication group.
//
// A group has one primary, zero or more in-sync replicas (the "sync set")
// and zero or more catching-up replicas. A write is assigned the next
// contiguous sequence number and processed by the primary immediately; acks
// mark it processed by other members. The global checkpoint (gcp) is the
// minimum lcp over the sync set and never decreases. An operation is
// confirmed once every member of the current sync set has processed it;
// confirmation is permanent and, unlike the gcp, may advance out of order.
package tracker

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/seqno"
)

// Sentinel errors. Every rejection maps to exactly one of these, so callers
// can distinguish causes with errors.Is.
var (
	// ErrInvalidArgument: malformed name, out-of-range seq/term or a term
	// exceeding the current term.
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrMember: a named member does not exist (or already exists).
	ErrMember = errors.New("member not found or already exists")
	// ErrStaleTerm: an ack carried a term older than the group's term.
	ErrStaleTerm = errors.New("stale term")
	// ErrState: the named member is in the wrong role for the operation.
	ErrState = errors.New("state mismatch")
	// ErrCaughtUp: a catching-up replica is not yet eligible to join the
	// sync set.
	ErrCaughtUp = errors.New("replica not caught up")
)

// Role classifies a group member.
type Role int

const (
	RolePrimary Role = iota
	RoleSync
	RoleCatchup
)

// Op is one processed operation: (seq, term, body). Noop reports a
// term-advancing no-operation entry created during promotion.
type Op struct {
	Seq  int64
	Term int64
	Body string
	Noop bool
}

type member struct {
	name string
	role Role
	hist map[int64]Op
	set  *seqno.Set
}

// Group is a replication group. All methods are safe for concurrent use and
// linearize to some serial execution.
type Group struct {
	mu      sync.Mutex
	members map[string]*member
	order   []*member // live members in insertion order
	primary string
	term    int64
	maxSeq  int64
	gcp     int64

	confirmed     map[int64]bool
	confirmedList []int64
	pending       map[int64]bool // real seqs not yet confirmed
	candidates    []int64        // seqs to evaluate in the current step
	evalAll       bool           // evaluate every pending seq (membership change)
	lost          []int64

	// PromotionBuilder builds a promotion plan from a locked group snapshot.
	// Package promote supplies the pure builder.
	PromotionBuilder func(view PromotionView) PromotionPlan
}

// validName enforces 1..64 byte names.
func validName(name string) bool {
	n := len(name)
	return n >= 1 && n <= 64
}

// New creates a group. primary leads; replicas are initial in-sync replicas.
// All names must be 1..64 bytes and pairwise distinct, and the group must
// contain 1..16 members.
func New(primary string, replicas []string) (*Group, error) {
	if !validName(primary) {
		return nil, fmt.Errorf("%w: primary name length out of range", ErrInvalidArgument)
	}
	names := map[string]struct{}{primary: {}}
	for _, r := range replicas {
		if !validName(r) {
			return nil, fmt.Errorf("%w: replica name length out of range", ErrInvalidArgument)
		}
		if _, dup := names[r]; dup {
			return nil, fmt.Errorf("%w: duplicate member %q", ErrInvalidArgument, r)
		}
		names[r] = struct{}{}
	}
	total := 1 + len(replicas)
	if total < 1 || total > 16 {
		return nil, fmt.Errorf("%w: member count %d out of range 1..16", ErrInvalidArgument, total)
	}

	g := &Group{
		members:   make(map[string]*member, total),
		primary:   primary,
		term:      1,
		confirmed: make(map[int64]bool),
		pending:   make(map[int64]bool),
	}
	p := &member{name: primary, role: RolePrimary, hist: make(map[int64]Op), set: seqno.New()}
	g.members[primary] = p
	g.order = append(g.order, p)
	for _, r := range replicas {
		m := &member{name: r, role: RoleSync, hist: make(map[int64]Op), set: seqno.New()}
		g.members[r] = m
		g.order = append(g.order, m)
	}
	return g, nil
}

// Term returns the current term.
func (g *Group) Term() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.term
}

// GCP returns the global checkpoint.
func (g *Group) GCP() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.gcp
}

// MaxSeq returns the primary's highest assigned sequence number.
func (g *Group) MaxSeq() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.maxSeq
}

// Primary returns the current primary name.
func (g *Group) Primary() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.primary
}

// Confirmed returns confirmed real-operation seqs in confirmation order.
func (g *Group) Confirmed() []int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]int64, len(g.confirmedList))
	copy(out, g.confirmedList)
	return out
}

// Lost returns seqs discarded by promotions, ascending.
func (g *Group) Lost() []int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]int64, len(g.lost))
	copy(out, g.lost)
	return out
}

// Role reports a member's role; ok is false if no such member exists.
func (g *Group) Role(name string) (Role, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	m, ok := g.members[name]
	if !ok {
		return 0, false
	}
	return m.role, true
}

// LCP returns a member's local checkpoint.
func (g *Group) LCP(name string) (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	m, ok := g.members[name]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrMember, name)
	}
	return m.set.LCP(), nil
}

// Steps returns the summed lcp-forward-step counts of all live members. In
// sequences without promotions the total is bounded by the number of
// accepted first acks plus the number of writes, independent of ack
// reordering. Intended as a white-box proof instrument.
func (g *Group) Steps() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	var total int64
	for _, m := range g.order {
		total += m.set.Steps()
	}
	return total
}

// History returns the member's processed entries in seq order.
func (g *Group) History(name string) ([]Op, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	m, ok := g.members[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrMember, name)
	}
	seqs := make([]int64, 0, len(m.hist))
	for seq := range m.hist {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	out := make([]Op, 0, len(seqs))
	for _, seq := range seqs {
		out = append(out, m.hist[seq])
	}
	return out, nil
}

// Write assigns the next sequence number to body and processes it on the
// primary. It returns the assigned seq.
func (g *Group) Write(body string) (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.maxSeq++
	seq := g.maxSeq
	op := Op{Seq: seq, Term: g.term, Body: body}
	p := g.members[g.primary]
	p.hist[seq] = op
	p.set.Set(seq)
	g.pending[seq] = true
	g.candidates = append(g.candidates, seq)
	g.recomputeLocked()
	return seq, nil
}

// Ack records that member processed seq while at term. A repeated ack for the
// same seq is idempotent success.
func (g *Group) Ack(name string, seq, term int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Rejection order: invalid argument > unknown member > stale term >
	// state mismatch. term > current term is an invalid argument.
	if !validName(name) || seq < 1 || seq > g.maxSeq || term > g.term {
		return fmt.Errorf("%w: ack out of range", ErrInvalidArgument)
	}
	m, ok := g.members[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrMember, name)
	}
	if term < g.term {
		return fmt.Errorf("%w: ack term %d < %d", ErrStaleTerm, term, g.term)
	}
	if m.role == RolePrimary {
		return fmt.Errorf("%w: cannot ack to primary %q", ErrState, name)
	}

	if _, have := m.hist[seq]; !have {
		// First processing of this seq by this member. The entry version is
		// the highest-term version the group currently carries at seq (a
		// later term always supersedes an earlier one); with no known
		// version, a current-term entry is recorded.
		var op Op
		found := false
		for _, other := range g.order {
			if other == m {
				continue
			}
			if e, ok := other.hist[seq]; ok && (!found || e.Term > op.Term) {
				op = e
				found = true
			}
		}
		if !found {
			op = Op{Seq: seq, Term: term}
		}
		m.hist[seq] = op
	}
	if m.set.Set(seq) {
		g.candidates = append(g.candidates, seq)
		g.recomputeLocked()
	}
	return nil
}

// AddReplica adds an empty catching-up replica.
func (g *Group) AddReplica(name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !validName(name) || len(g.order) >= 16 {
		return fmt.Errorf("%w: name or group size out of range", ErrInvalidArgument)
	}
	if _, ok := g.members[name]; ok {
		return fmt.Errorf("%w: %q already exists", ErrMember, name)
	}
	m := &member{name: name, role: RoleCatchup, hist: make(map[int64]Op), set: seqno.New()}
	g.members[name] = m
	g.order = append(g.order, m)
	return nil
}

// MarkInSync moves a caught-up replica into the sync set. It requires
// lcp >= gcp and that every confirmed operation has been processed.
func (g *Group) MarkInSync(name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !validName(name) {
		return fmt.Errorf("%w: name length out of range", ErrInvalidArgument)
	}
	m, ok := g.members[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrMember, name)
	}
	if m.role != RoleCatchup {
		return fmt.Errorf("%w: %q is not a catching-up replica", ErrState, name)
	}
	if m.set.LCP() < g.gcp {
		return fmt.Errorf("%w: lcp %d < gcp %d", ErrCaughtUp, m.set.LCP(), g.gcp)
	}
	for seq := range g.confirmed {
		if !m.set.Has(seq) {
			return fmt.Errorf("%w: confirmed seq %d not processed", ErrCaughtUp, seq)
		}
	}
	m.role = RoleSync
	g.evalAll = true
	g.recomputeLocked()
	return nil
}

// FailReplica removes a non-primary member from the group.
func (g *Group) FailReplica(name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !validName(name) {
		return fmt.Errorf("%w: name length out of range", ErrInvalidArgument)
	}
	m, ok := g.members[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrMember, name)
	}
	if m.role == RolePrimary {
		return fmt.Errorf("%w: cannot fail primary %q", ErrState, name)
	}

	delete(g.members, name)
	for i, mm := range g.order {
		if mm == m {
			g.order = append(g.order[:i], g.order[i+1:]...)
			break
		}
	}
	g.evalAll = true
	g.recomputeLocked()
	return nil
}

// recomputeLocked updates the gcp and appends newly confirmed operations.
// Callers hold mu.
//
// gcp is the minimum lcp over primary plus in-sync replicas and never
// decreases. A real (non-noop) seq processed by every sync member is
// confirmed permanently; confirmation is decided per seq independently, so a
// high seq can confirm while holes remain. Only seqs that changed during the
// current step (candidates) are re-evaluated; membership changes set evalAll
// and evaluate all pending seqs in ascending order, which also fixes the
// ascending append order when several confirm in one step. Each seq leaves
// the pending set once confirmed, so total evaluation work is linear in
// accepted first acks plus writes plus membership events.
func (g *Group) recomputeLocked() {
	var minLCP int64 = -1
	for _, m := range g.order {
		if m.role != RolePrimary && m.role != RoleSync {
			continue
		}
		l := m.set.LCP()
		if minLCP < 0 || l < minLCP {
			minLCP = l
		}
	}
	if minLCP > g.gcp {
		g.gcp = minLCP
	}

	cand := g.candidates
	g.candidates = nil
	if g.evalAll {
		cand = cand[:0]
		for seq := range g.pending {
			cand = append(cand, seq)
		}
		sortInts(cand)
		g.evalAll = false
	}
	for _, seq := range cand {
		if !g.pending[seq] {
			continue
		}
		var entry Op
		all := true
		for _, m := range g.order {
			if m.role != RolePrimary && m.role != RoleSync {
				continue
			}
			e, ok := m.hist[seq]
			if !ok {
				all = false
				break
			}
			entry = e
		}
		if all {
			delete(g.pending, seq)
			if !entry.Noop && !g.confirmed[seq] {
				g.confirmed[seq] = true
				g.confirmedList = append(g.confirmedList, seq)
			}
		}
	}
}

// PlanEntry describes one entry installed on a member during promotion.
type PlanEntry struct {
	Seq  int64
	Term int64
	Body string
	Noop bool
}

// PromotionPlan is the pure transformation produced by a promotion builder.
type PromotionPlan struct {
	NewTerm    int64
	G, M       int64
	NewPrimary string
	// Holes fill gaps in 1..M of the new primary (term-advancing noops).
	Holes []PlanEntry
	// Resync are entries g+1..M installed on every other sync replica.
	Resync []PlanEntry
	// Lost lists assigned seqs absent from the new primary's history,
	// ascending: hole positions and seqs strictly greater than M.
	Lost []int64
}

// PromotionView is the immutable snapshot handed to the promotion builder.
type PromotionView struct {
	OldPrimary string
	Candidate  string
	Term       int64
	G          int64 // gcp before promotion
	MaxSeq     int64 // primary's highest assigned seq
	M          int64 // candidate's highest processed seq (0 if none)

	// Canonical[seq] is the agreed entry at seq <= G.
	Canonical map[int64]Op
	// NewPrimary[seq] is the candidate's entry at seq, when present.
	NewPrimary map[int64]Op
	// Confirmed[seq] reports confirmed real operations.
	Confirmed map[int64]bool
	// Sync lists sync-set members including the primary and the candidate.
	Sync []string
	// Catchup lists catching-up members.
	Catchup []string
}

// RunPromotion promotes name to primary. The plan is built from a locked
// snapshot by the configured PromotionBuilder (package promote) and applied
// atomically; no concurrent operation can interleave.
func (g *Group) RunPromotion(name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Rejection order: invalid argument > unknown member > (no term check)
	// > state mismatch: the target must be an in-sync replica.
	if !validName(name) {
		return fmt.Errorf("%w: name length out of range", ErrInvalidArgument)
	}
	m, ok := g.members[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrMember, name)
	}
	if m.role != RoleSync {
		return fmt.Errorf("%w: %q is not an in-sync replica", ErrState, name)
	}
	if g.PromotionBuilder == nil {
		return fmt.Errorf("%w: promotion builder not installed", ErrState)
	}

	view := g.snapshotLocked(name)
	plan := g.PromotionBuilder(view)
	g.applyPlanLocked(m, view, plan)
	return nil
}

func (g *Group) snapshotLocked(candidate string) PromotionView {
	v := PromotionView{
		OldPrimary: g.primary,
		Candidate:  candidate,
		Term:       g.term,
		G:          g.gcp,
		MaxSeq:     g.maxSeq,
		Canonical:  make(map[int64]Op),
		NewPrimary: make(map[int64]Op),
		Confirmed:  make(map[int64]bool, len(g.confirmed)),
	}
	for seq := range g.confirmed {
		v.Confirmed[seq] = true
	}
	for _, mm := range g.order {
		switch mm.role {
		case RolePrimary, RoleSync:
			v.Sync = append(v.Sync, mm.name)
		case RoleCatchup:
			v.Catchup = append(v.Catchup, mm.name)
		}
		if mm.name == candidate {
			v.M = mm.set.Max()
			for seq, op := range mm.hist {
				v.NewPrimary[seq] = op
			}
		}
	}
	for seq := int64(1); seq <= v.G; seq++ {
		if e, ok := g.members[g.primary].hist[seq]; ok {
			v.Canonical[seq] = e
		}
	}
	return v
}

func (g *Group) applyPlanLocked(cand *member, v PromotionView, p PromotionPlan) {
	// 1. New primary: fill holes in 1..M with term-advancing noops.
	for _, e := range p.Holes {
		cand.hist[e.Seq] = Op{Seq: e.Seq, Term: e.Term, Noop: true}
		cand.set.Set(e.Seq)
	}
	// 2. Every other current sync replica: roll back above g, resync g+1..M.
	for _, mm := range g.order {
		if mm.role != RoleSync || mm == cand {
			continue
		}
		mm.set.TruncateAbove(p.G)
		for seq := range mm.hist {
			if seq > p.G {
				delete(mm.hist, seq)
			}
		}
		for _, e := range p.Resync {
			op := Op{Seq: e.Seq, Term: e.Term, Body: e.Body, Noop: e.Noop}
			mm.hist[e.Seq] = op
			mm.set.Set(e.Seq)
		}
	}
	// 3. Catching-up replicas: only discard above g; they keep catching up.
	for _, mm := range g.order {
		if mm.role != RoleCatchup {
			continue
		}
		mm.set.TruncateAbove(p.G)
		for seq := range mm.hist {
			if seq > p.G {
				delete(mm.hist, seq)
			}
		}
	}
	// 4. Old primary leaves the group.
	old := g.members[g.primary]
	delete(g.members, g.primary)
	for i, mm := range g.order {
		if mm == old {
			g.order = append(g.order[:i], g.order[i+1:]...)
			break
		}
	}

	// 5. Roles, term, sequence window.
	cand.role = RolePrimary
	g.primary = cand.name
	g.term = p.NewTerm
	g.maxSeq = p.M

	// 6. Lost seqs leave the unconfirmed pending set.
	for _, seq := range p.Lost {
		// An operation confirmed before promotion can never be lost; a hole
		// at such a seq cannot occur given MarkInSync/Promote eligibility,
		// but guard the invariant regardless.
		if g.confirmed[seq] {
			continue
		}
		delete(g.pending, seq)
		g.lost = append(g.lost, seq)
	}

	// 7. Recompute gcp and confirmations over the new sync set; remaining
	// pending seqs confirmed during this step are appended ascending.
	g.evalAll = true
	g.candidates = nil
	g.recomputeLocked()
}

// sortInts sorts ascending.
func sortInts(a []int64) { sort.Slice(a, func(i, j int) bool { return a[i] < a[j] }) }
