package promote_test

// Table-driven promotion tests plus the 1500-scenario randomized equivalence
// suite (with an independent naive simulation).

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/promote"
	"ontology/tracker"
)

func setupExample(t *testing.T) *tracker.Group {
	t.Helper()
	g, err := promote.New("P", []string{"R1", "R2"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := g.Write("b"); err != nil {
			t.Fatal(err)
		}
	}
	g.Ack("R1", 1, 1)
	g.Ack("R1", 3, 1)
	g.Ack("R2", 1, 1)
	g.Ack("R2", 2, 1)
	g.Ack("R2", 3, 1)
	return g
}

// TestPromoteWorkedExample reproduces branch乙 of the spec.
func TestPromoteWorkedExample(t *testing.T) {
	g := setupExample(t)
	if err := promote.Promote(g, "R1"); err != nil {
		t.Fatal(err)
	}
	if g.Term() != 2 {
		t.Fatalf("term = %d, want 2", g.Term())
	}
	if g.GCP() != 3 {
		t.Fatalf("gcp = %d, want 3", g.GCP())
	}
	if got := g.Lost(); len(got) != 1 || got[0] != 2 {
		t.Fatalf("lost = %v, want [2]", got)
	}
	if got := g.Confirmed(); len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("confirmed = %v, want [1 3]", got)
	}
	if g.Primary() != "R1" {
		t.Fatalf("primary = %q", g.Primary())
	}

	h, err := g.History("R2")
	if err != nil {
		t.Fatal(err)
	}
	want := []tracker.Op{
		{Seq: 1, Term: 1, Body: "b"},
		{Seq: 2, Term: 2, Noop: true},
		{Seq: 3, Term: 1, Body: "b"},
	}
	if len(h) != len(want) {
		t.Fatalf("R2 history len = %d, want %d: %+v", len(h), len(want), h)
	}
	for i := range want {
		if h[i] != want[i] {
			t.Fatalf("R2 history[%d] = %+v, want %+v", i, h[i], want[i])
		}
	}
	hp, err := g.History("R1")
	if err != nil || !hp[1].Noop || hp[1].Term != 2 {
		t.Fatalf("new primary history = %v err=%v", hp, err)
	}

	// Next write is seq 4.
	seq, err := g.Write("c")
	if err != nil || seq != 4 {
		t.Fatalf("write = %d, %v, want 4", seq, err)
	}

	// Old-term ack is rejected as stale; old primary no longer exists.
	if err := g.Ack("R2", 3, 1); !errors.Is(err, tracker.ErrStaleTerm) {
		t.Fatalf("old term ack: %v", err)
	}
	if err := g.Ack("P", 3, 2); !errors.Is(err, tracker.ErrMember) {
		t.Fatalf("ack old primary: %v", err)
	}
}

func TestPromoteConfirmedLostDisjoint(t *testing.T) {
	g := setupExample(t)
	if err := promote.Promote(g, "R1"); err != nil {
		t.Fatal(err)
	}
	for _, seq := range g.Confirmed() {
		for _, l := range g.Lost() {
			if seq == l {
				t.Fatalf("seq %d both confirmed and lost", seq)
			}
		}
	}
}

func TestPromoteKeepsCatchupRollback(t *testing.T) {
	g := setupExample(t)
	if err := g.AddReplica("R3"); err != nil {
		t.Fatal(err)
	}
	// R3 processed past g (only seq 1 is below gcp, give it 1 and 2):
	if err := g.Ack("R3", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := g.Ack("R3", 2, 1); err != nil {
		t.Fatal(err)
	}
	if err := promote.Promote(g, "R1"); err != nil {
		t.Fatal(err)
	}
	lcp, err := g.LCP("R3")
	if err != nil {
		t.Fatal(err)
	}
	if lcp != 1 {
		t.Fatalf("catchup lcp = %d, want 1 (rolled back to g)", lcp)
	}
	if role, ok := g.Role("R3"); !ok || role != tracker.RoleCatchup {
		t.Fatalf("R3 role = %v, %v", role, ok)
	}
	h, _ := g.History("R3")
	if len(h) != 1 || h[0].Seq != 1 {
		t.Fatalf("R3 history = %v", h)
	}
}

func TestPromoteRejections(t *testing.T) {
	g := setupExample(t)
	if err := g.AddReplica("R3"); err != nil {
		t.Fatal(err)
	}
	if err := promote.Promote(g, ""); !errors.Is(err, tracker.ErrInvalidArgument) {
		t.Fatalf("bad name: %v", err)
	}
	if err := promote.Promote(g, "Ghost"); !errors.Is(err, tracker.ErrMember) {
		t.Fatalf("unknown: %v", err)
	}
	// Promoting the primary is a state mismatch (not an in-sync replica).
	if err := promote.Promote(g, "P"); !errors.Is(err, tracker.ErrState) {
		t.Fatalf("promote primary: %v", err)
	}
	// Catch-up replica cannot be promoted.
	if err := promote.Promote(g, "R3"); !errors.Is(err, tracker.ErrState) {
		t.Fatalf("promote catchup: %v", err)
	}
	// Failed promotion changes nothing.
	if g.Term() != 1 || g.Primary() != "P" {
		t.Fatalf("state changed after rejected promote: term=%d primary=%s", g.Term(), g.Primary())
	}
}

// TestSecondPromotion exercises term bumping again and noop at higher term.
func TestSecondPromotion(t *testing.T) {
	g := setupExample(t)
	if err := promote.Promote(g, "R2"); err != nil {
		t.Fatal(err)
	}
	// R2 has 1,2,3 -> no holes, nothing lost; R1 rolls back to g=1 and
	// resyncs 2,3.
	if len(g.Lost()) != 0 {
		t.Fatalf("lost = %v", g.Lost())
	}
	lcp, _ := g.LCP("R1")
	if lcp != 3 {
		t.Fatalf("R1 lcp = %d", lcp)
	}
	// Now write 4 (only new primary R2 has it), promote R1: M=3, seq 4 lost.
	if _, err := g.Write("d"); err != nil {
		t.Fatal(err)
	}
	if err := promote.Promote(g, "R1"); err != nil {
		t.Fatal(err)
	}
	if g.Term() != 3 {
		t.Fatalf("term = %d", g.Term())
	}
	if lost := g.Lost(); len(lost) != 1 || lost[0] != 4 {
		t.Fatalf("lost = %v, want [4]", lost)
	}
	if seq, _ := g.Write("e"); seq != 4 {
		t.Fatalf("after truncation next seq = %d, want 4", seq)
	}
}

func TestPromoteEmptyCandidate(t *testing.T) {
	// Candidate with nothing processed: g must be 0 here; construct a group
	// where gcp is 0 (write to solo-ish sync member that acked nothing is
	// impossible: gcp 0 is valid with a replica that acked nothing, but
	// promotion requires in-sync which exists from New). M=0, all seqs lost.
	g, err := promote.New("P", []string{"R1"})
	if err != nil {
		t.Fatal(err)
	}
	g.Write("a") // gcp stays 0
	if err := promote.Promote(g, "R1"); err != nil {
		t.Fatal(err)
	}
	if g.GCP() != 0 {
		t.Fatalf("gcp = %d, want 0", g.GCP())
	}
	if lost := g.Lost(); len(lost) != 1 || lost[0] != 1 {
		t.Fatalf("lost = %v, want [1]", lost)
	}
}

// Independent step-by-step naive simulation of the specification. It is
// written from the prose rules without sharing implementation code with the
// packages under test: every step recomputes gcp and confirmation from
// scratch over 1..maxSeq (the deliberately quadratic baseline).

type naiveOp struct {
	term int64
	body string
	noop bool
}

type naiveMember struct {
	role      tracker.Role
	processed map[int64]bool
	hist      map[int64]naiveOp
}

type naiveGroup struct {
	members   map[string]*naiveMember
	order     []string
	primary   string
	term      int64
	maxSeq    int64
	gcp       int64
	confirmed map[int64]bool
	confList  []int64
	lost      []int64
}

type errClass int

const (
	ecOK errClass = iota
	ecInvalid
	ecMember
	ecStale
	ecState
	ecCaughtUp
)

func naiveNew(primary string, reps []string) *naiveGroup {
	g := &naiveGroup{
		members:   map[string]*naiveMember{},
		primary:   primary,
		term:      1,
		confirmed: map[int64]bool{},
	}
	g.members[primary] = newNaiveMember(tracker.RolePrimary)
	g.order = append(g.order, primary)
	for _, r := range reps {
		g.members[r] = newNaiveMember(tracker.RoleSync)
		g.order = append(g.order, r)
	}
	return g
}

func newNaiveMember(role tracker.Role) *naiveMember {
	return &naiveMember{
		role:      role,
		processed: map[int64]bool{},
		hist:      map[int64]naiveOp{},
	}
}

func (g *naiveGroup) liveNames2() []string { return append([]string(nil), g.order...) }

func (g *naiveGroup) lcp(m *naiveMember) int64 {
	k := int64(0)
	for m.processed[k+1] {
		k++
	}
	return k
}

func (g *naiveGroup) syncMembers() []*naiveMember {
	var out []*naiveMember
	for _, n := range g.order {
		m := g.members[n]
		if m.role == tracker.RolePrimary || m.role == tracker.RoleSync {
			out = append(out, m)
		}
	}
	return out
}

func classify(err error) errClass {
	switch {
	case err == nil:
		return ecOK
	case errors.Is(err, tracker.ErrInvalidArgument):
		return ecInvalid
	case errors.Is(err, tracker.ErrMember):
		return ecMember
	case errors.Is(err, tracker.ErrStaleTerm):
		return ecStale
	case errors.Is(err, tracker.ErrState):
		return ecState
	case errors.Is(err, tracker.ErrCaughtUp):
		return ecCaughtUp
	default:
		return -1
	}
}

func naiveValidName(n string) bool { return len(n) >= 1 && len(n) <= 64 }

func (g *naiveGroup) write(body string) (int64, errClass) {
	g.maxSeq++
	seq := g.maxSeq
	p := g.members[g.primary]
	p.processed[seq] = true
	p.hist[seq] = naiveOp{term: g.term, body: body}
	g.recompute()
	return seq, ecOK
}

func (g *naiveGroup) ack(name string, seq, term int64) errClass {
	if !naiveValidName(name) || seq < 1 || seq > g.maxSeq || term > g.term {
		return ecInvalid
	}
	m, ok := g.members[name]
	if !ok {
		return ecMember
	}
	if term < g.term {
		return ecStale
	}
	if m.role == tracker.RolePrimary {
		return ecState
	}
	first := !m.processed[seq]
	if first {
		var best naiveOp
		have := false
		for _, n := range g.order {
			other := g.members[n]
			if other == m {
				continue
			}
			if e, ok := other.hist[seq]; ok && (!have || e.term > best.term) {
				best, have = e, true
			}
		}
		if !have {
			best = naiveOp{term: term}
		}
		m.hist[seq] = best
	}
	m.processed[seq] = true
	if first {
		g.recompute()
	}
	return ecOK
}

func (g *naiveGroup) addReplica(name string) errClass {
	if !naiveValidName(name) || len(g.order) >= 16 {
		return ecInvalid
	}
	if _, ok := g.members[name]; ok {
		return ecMember
	}
	g.members[name] = newNaiveMember(tracker.RoleCatchup)
	g.order = append(g.order, name)
	return ecOK
}

func (g *naiveGroup) markInSync(name string) errClass {
	if !naiveValidName(name) {
		return ecInvalid
	}
	m, ok := g.members[name]
	if !ok {
		return ecMember
	}
	if m.role != tracker.RoleCatchup {
		return ecState
	}
	if g.lcp(m) < g.gcp {
		return ecCaughtUp
	}
	for seq := range g.confirmed {
		if !m.processed[seq] {
			return ecCaughtUp
		}
	}
	m.role = tracker.RoleSync
	g.recompute()
	return ecOK
}

func (g *naiveGroup) failReplica(name string) errClass {
	if !naiveValidName(name) {
		return ecInvalid
	}
	m, ok := g.members[name]
	if !ok {
		return ecMember
	}
	if m.role == tracker.RolePrimary {
		return ecState
	}
	delete(g.members, name)
	for i, n := range g.order {
		if n == name {
			g.order = append(g.order[:i], g.order[i+1:]...)
			break
		}
	}
	_ = m
	g.recompute()
	return ecOK
}

func (g *naiveGroup) promote(name string) errClass {
	if !naiveValidName(name) {
		return ecInvalid
	}
	cand, ok := g.members[name]
	if !ok {
		return ecMember
	}
	if cand.role != tracker.RoleSync {
		return ecState
	}

	beforeG := g.gcp
	newTerm := g.term + 1
	var mVal int64
	for seq := range cand.processed {
		if seq > mVal {
			mVal = seq
		}
	}

	var holes, lost []int64
	for seq := int64(1); seq <= mVal; seq++ {
		if !cand.processed[seq] {
			holes = append(holes, seq)
			lost = append(lost, seq)
		}
	}
	for seq := mVal + 1; seq <= g.maxSeq; seq++ {
		lost = append(lost, seq)
	}

	for _, seq := range holes {
		cand.processed[seq] = true
		cand.hist[seq] = naiveOp{term: newTerm, noop: true}
	}

	for _, n := range g.order {
		m := g.members[n]
		if m == cand {
			continue
		}
		switch m.role {
		case tracker.RoleSync:
			for seq := range m.processed {
				if seq > beforeG {
					delete(m.processed, seq)
					delete(m.hist, seq)
				}
			}
			for seq := beforeG + 1; seq <= mVal; seq++ {
				m.processed[seq] = true
				m.hist[seq] = cand.hist[seq]
			}
		case tracker.RoleCatchup:
			for seq := range m.processed {
				if seq > beforeG {
					delete(m.processed, seq)
					delete(m.hist, seq)
				}
			}
		}
	}

	oldName := g.primary
	delete(g.members, oldName)
	for i, n := range g.order {
		if n == oldName {
			g.order = append(g.order[:i], g.order[i+1:]...)
			break
		}
	}
	cand.role = tracker.RolePrimary
	g.primary = name
	g.term = newTerm
	g.maxSeq = mVal
	for _, seq := range lost {
		// Invariant: confirmed operations are never lost.
		if !g.confirmed[seq] {
			g.lost = append(g.lost, seq)
		}
	}
	g.recompute()
	return ecOK
}

// recompute is the deliberately naive O(maxSeq) rescan after every step.
func (g *naiveGroup) recompute() {
	minL := int64(-1)
	for _, m := range g.syncMembers() {
		l := g.lcp(m)
		if minL < 0 || l < minL {
			minL = l
		}
	}
	if minL > g.gcp {
		g.gcp = minL
	}
	syncs := g.syncMembers()
	var newly []int64
	for seq := int64(1); seq <= g.maxSeq; seq++ {
		if g.confirmed[seq] {
			continue
		}
		all := true
		noop := false
		for _, m := range syncs {
			if !m.processed[seq] {
				all = false
				break
			}
			if e, ok := m.hist[seq]; ok && e.noop {
				// Sync members agree below the gcp; a noop entry never
				// enters Confirmed.
				noop = true
			}
		}
		if all && !noop {
			g.confirmed[seq] = true
			newly = append(newly, seq)
		}
	}
	sort.Slice(newly, func(i, j int) bool { return newly[i] < newly[j] })
	g.confList = append(g.confList, newly...)
}

func (g *naiveGroup) history(name string) ([]tracker.Op, bool) {
	m, ok := g.members[name]
	if !ok {
		return nil, false
	}
	var seqs []int64
	for seq := range m.hist {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	out := make([]tracker.Op, 0, len(seqs))
	for _, seq := range seqs {
		e := m.hist[seq]
		out = append(out, tracker.Op{Seq: seq, Term: e.term, Body: e.body, Noop: e.noop})
	}
	return out, true
}

// 1500 random operation sequences compared step by step against the
// independent naive simulation, with input/output transcripts.

type opKind int

const (
	opWrite opKind = iota
	opAck
	opAddReplica
	opMarkInSync
	opFailReplica
	opPromote
)

type stepOp struct {
	kind opKind
	name string
	seq  int64
	term int64
	body string
}

func (o stepOp) String() string {
	switch o.kind {
	case opWrite:
		return fmt.Sprintf("Write(%q)", o.body)
	case opAck:
		return fmt.Sprintf("Ack(%s,%d,%d)", o.name, o.seq, o.term)
	case opAddReplica:
		return fmt.Sprintf("AddReplica(%q)", o.name)
	case opMarkInSync:
		return fmt.Sprintf("MarkInSync(%q)", o.name)
	case opFailReplica:
		return fmt.Sprintf("FailReplica(%q)", o.name)
	case opPromote:
		return fmt.Sprintf("Promote(%q)", o.name)
	}
	return "?"
}

type snapshot struct {
	term, gcp, maxSeq int64
	primary           string
	confirmed         []int64
	lost              []int64
	members           map[string]memSnap
}

type memSnap struct {
	role tracker.Role
	lcp  int64
	hist []tracker.Op
}

var trackedNames = []string{"P", "R1", "R2", "R3", "R4"}

func snapReal(g *tracker.Group) snapshot {
	s := snapshot{
		term: g.Term(), gcp: g.GCP(), maxSeq: g.MaxSeq(), primary: g.Primary(),
		confirmed: g.Confirmed(), lost: g.Lost(),
		members: map[string]memSnap{},
	}
	for _, n := range trackedNames {
		if role, ok := g.Role(n); ok {
			lcp, _ := g.LCP(n)
			h, _ := g.History(n)
			s.members[n] = memSnap{role: role, lcp: lcp, hist: h}
		}
	}
	return s
}

func snapNaive(n *naiveGroup) snapshot {
	s := snapshot{
		term: n.term, gcp: n.gcp, maxSeq: n.maxSeq, primary: n.primary,
		confirmed: append([]int64(nil), n.confList...),
		lost:      append([]int64(nil), n.lost...),
		members:   map[string]memSnap{},
	}
	for _, name := range n.liveNames2() {
		m := n.members[name]
		h, _ := n.history(name)
		s.members[name] = memSnap{role: m.role, lcp: n.lcp(m), hist: h}
	}
	return s
}

func intsEq(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func opsEq(a, b []tracker.Op) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func compareSnap(want, got snapshot) string {
	var b strings.Builder
	check := func(cond bool, msg string) {
		if !cond {
			fmt.Fprintf(&b, "; %s", msg)
		}
	}
	check(want.term == got.term, fmt.Sprintf("term naive=%d real=%d", want.term, got.term))
	check(want.gcp == got.gcp, fmt.Sprintf("gcp naive=%d real=%d", want.gcp, got.gcp))
	check(want.maxSeq == got.maxSeq, fmt.Sprintf("maxSeq naive=%d real=%d", want.maxSeq, got.maxSeq))
	check(want.primary == got.primary, fmt.Sprintf("primary naive=%q real=%q", want.primary, got.primary))
	check(intsEq(want.confirmed, got.confirmed), fmt.Sprintf("confirmed naive=%v real=%v", want.confirmed, got.confirmed))
	check(intsEq(want.lost, got.lost), fmt.Sprintf("lost naive=%v real=%v", want.lost, got.lost))
	check(len(want.members) == len(got.members),
		fmt.Sprintf("member set naive=%v real=%v", memberKeys(want.members), memberKeys(got.members)))
	for name, wm := range want.members {
		gm, ok := got.members[name]
		if !ok {
			continue
		}
		check(wm.role == gm.role, fmt.Sprintf("%s role naive=%d real=%d", name, wm.role, gm.role))
		check(wm.lcp == gm.lcp, fmt.Sprintf("%s lcp naive=%d real=%d", name, wm.lcp, gm.lcp))
		check(opsEq(wm.hist, gm.hist), fmt.Sprintf("%s hist naive=%+v real=%+v", name, wm.hist, gm.hist))
	}
	return b.String()
}

func memberKeys(m map[string]memSnap) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// invariants checks global properties on the post-step snapshot, plus the
// exact "a confirmed operation is never lost" rule evaluated on the delta of
// this step. Sequence numbers can be reused after promotion truncates
// maxSeq, so the comparison must be between the seqs newly appended to Lost
// during this step and the confirmation list that existed before it.
func checkInvariants(before, after snapshot) string {
	var b strings.Builder
	wasConfirmed := map[int64]bool{}
	for _, c := range before.confirmed {
		wasConfirmed[c] = true
	}
	for i := len(before.lost); i < len(after.lost); i++ {
		seq := after.lost[i]
		if wasConfirmed[seq] {
			fmt.Fprintf(&b, "; seq %d appended to Lost was confirmed before the step (%v)", seq, before.confirmed)
		}
	}
	confSet := map[int64]bool{}
	for _, c := range after.confirmed {
		confSet[c] = true
	}
	var sync []string
	for n, m := range after.members {
		if m.role == tracker.RolePrimary || m.role == tracker.RoleSync {
			sync = append(sync, n)
		}
	}
	for _, n := range sync {
		m := after.members[n]
		for _, op := range m.hist {
			if op.Seq <= after.gcp && !op.Noop && !confSet[op.Seq] {
				fmt.Fprintf(&b, "; %s has real seq %d <= gcp %d but unconfirmed", n, op.Seq, after.gcp)
			}
		}
	}
	// Prefix histories agree entry-by-entry up to gcp.
	for i := 0; i < len(sync); i++ {
		for j := i + 1; j < len(sync); j++ {
			a, b2 := prefixOps(after.members[sync[i]].hist, after.gcp), prefixOps(after.members[sync[j]].hist, after.gcp)
			if !opsEq(a, b2) {
				fmt.Fprintf(&b, "; prefix mismatch %s=%+v %s=%+v at gcp %d", sync[i], a, sync[j], b2, after.gcp)
			}
		}
	}
	// gcp never decreases.
	if after.gcp < before.gcp {
		fmt.Fprintf(&b, "; gcp decreased %d -> %d", before.gcp, after.gcp)
	}
	return b.String()
}

func containsInt(xs []int64, x int64) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func prefixOps(h []tracker.Op, upto int64) []tracker.Op {
	var out []tracker.Op
	for _, op := range h {
		if op.Seq <= upto {
			out = append(out, op)
		}
	}
	return out
}

func genScenario(r *rand.Rand) []stepOp {
	n := 2 + r.Intn(3) // 2..4 initial members: P + R1..R{k}

	length := 20 + r.Intn(80)
	maxMembers := n
	var seq []stepOp
	for i := 0; i < length; i++ {
		switch r.Intn(10) {
		case 0, 1, 2, 3:
			seq = append(seq, stepOp{kind: opWrite, body: fmt.Sprintf("b%d", i)})
		case 4, 5, 6:
			name := trackedNames[1+r.Intn(maxMembers-1)]
			seq = append(seq, stepOp{kind: opAck, name: name, seq: int64(1 + r.Intn(40)), term: int64(r.Intn(5))})
		case 7:
			if maxMembers < len(trackedNames) {
				name := trackedNames[maxMembers]
				maxMembers++
				seq = append(seq, stepOp{kind: opAddReplica, name: name})
			} else {
				seq = append(seq, stepOp{kind: opWrite, body: "x"})
			}
		case 8:
			// Any existing tracked name including unknown candidates.
			name := trackedNames[r.Intn(len(trackedNames))]
			seq = append(seq, stepOp{kind: opMarkInSync, name: name})
		default:
			if r.Intn(3) == 0 {
				name := trackedNames[r.Intn(len(trackedNames))]
				seq = append(seq, stepOp{kind: opPromote, name: name})
			} else {
				name := trackedNames[r.Intn(len(trackedNames))]
				seq = append(seq, stepOp{kind: opFailReplica, name: name})
			}
		}
	}
	return seq
}

func runReal(g *tracker.Group, o stepOp) (string, errClass) {
	var err error
	var out string
	switch o.kind {
	case opWrite:
		var seq int64
		seq, err = g.Write(o.body)
		out = fmt.Sprintf("Write->%d", seq)
	case opAck:
		err = g.Ack(o.name, o.seq, o.term)
	case opAddReplica:
		err = g.AddReplica(o.name)
	case opMarkInSync:
		err = g.MarkInSync(o.name)
	case opFailReplica:
		err = g.FailReplica(o.name)
	case opPromote:
		err = promote.Promote(g, o.name)
	}
	if err != nil {
		out = "err=" + err.Error()
	}
	return out, classify(err)
}

func runNaive(g *naiveGroup, o stepOp) (string, errClass) {
	var c errClass
	var out string
	switch o.kind {
	case opWrite:
		var seq int64
		seq, c = g.write(o.body)
		out = fmt.Sprintf("Write->%d", seq)
	case opAck:
		c = g.ack(o.name, o.seq, o.term)
	case opAddReplica:
		c = g.addReplica(o.name)
	case opMarkInSync:
		c = g.markInSync(o.name)
	case opFailReplica:
		c = g.failReplica(o.name)
	case opPromote:
		c = g.promote(o.name)
	}
	if c != ecOK {
		out = "err=" + c.String()
	}
	return out, c
}

func (c errClass) String() string {
	switch c {
	case ecInvalid:
		return "invalid"
	case ecMember:
		return "member"
	case ecStale:
		return "stale"
	case ecState:
		return "state"
	case ecCaughtUp:
		return "caughtup"
	}
	return "ok"
}

func TestRandomEquivalence1500(t *testing.T) {
	for id := 0; id < 1500; id++ {
		r := rand.New(rand.NewSource(int64(id) + 1))
		n := 2 + r.Intn(3)
		reps := trackedNames[1:n]

		real, err := promote.New("P", reps)
		if err != nil {
			t.Fatal(err)
		}
		naive := naiveNew("P", reps)
		scenario := genScenario(r)

		var log strings.Builder
		fmt.Fprintf(&log, "scenario %d: New(P, %v)\n", id, reps)
		bad := false
		for step, o := range scenario {
			before := snapReal(real)
			realOut, realClass := runReal(real, o)
			naiveOut, naiveClass := runNaive(naive, o)
			fmt.Fprintf(&log, "step %d: %s -> real[%s class=%s] naive[%s class=%s]\n",
				step, o, realOut, realClass.String(), naiveOut, naiveClass.String())
			if realClass != naiveClass {
				fmt.Fprintf(&log, "JUDGEMENT: error class mismatch at step %d\n", step)
				bad = true
				break
			}
			rs, ns := snapReal(real), snapNaive(naive)
			if d := compareSnap(ns, rs); d != "" {
				fmt.Fprintf(&log, "JUDGEMENT: state mismatch at step %d%s\n", step, d)
				bad = true
				break
			}
			if d := checkInvariants(before, rs); d != "" {
				fmt.Fprintf(&log, "JUDGEMENT: invariant violation at step %d%s\n", step, d)
				bad = true
				break
			}
		}
		if bad {
			t.Fatalf("scenario %d diverged:\n%s", id, log.String())
		}
		if testing.Verbose() {
			t.Logf("scenario %d (%d steps): OK", id, len(scenario))
		}
	}
}
