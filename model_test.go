package ontology

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

type naivePeer struct {
	match        int
	next         int
	snapshot     int
	snapshotTerm int
	inFlight     bool
}

type naiveModel struct {
	T, Thr, L int
	log       []int
	last      int
	commit    int
	applied   int
	snapIndex int
	snapTerm  int
	baseIndex int
	baseTerm  int
	peers     map[string]*naivePeer
	removed   int
	logs      strings.Builder
}

type randomOp struct {
	name string
	arg  int
	id   string
}

func (op randomOp) String() string {
	if needsPeer(op.name) {
		return fmt.Sprintf("%s id=%q arg=%d", op.name, op.id, op.arg)
	}
	return fmt.Sprintf("%s arg=%d", op.name, op.arg)
}

func newNaive(T, Thr, L int) *naiveModel {
	return &naiveModel{T: T, Thr: Thr, L: L, peers: make(map[string]*naivePeer)}
}

func TestNaiveModelSkeleton(t *testing.T) {
	c, err := New(0, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	m := newNaive(0, 1, 0)
	if c == nil || m == nil {
		t.Fatal("nil coordinator")
	}
}

func (m *naiveModel) logf(format string, args ...any) {
	fmt.Fprintf(&m.logs, format+"\n", args...)
}

func (m *naiveModel) termAt(idx int) (int, error) {
	if idx < m.baseIndex {
		return 0, ErrCompacted
	}
	if idx == m.baseIndex {
		return m.baseTerm, nil
	}
	if idx > m.last {
		return 0, ErrRange
	}
	return m.log[idx-m.baseIndex-1], nil
}

func (m *naiveModel) appendLog(term int) (int, error) {
	if term < 1 {
		m.logf("input=append term=%d; output=ErrParam; reason=term<1", term)
		return 0, ErrParam
	}
	if m.last > 0 {
		lastTerm, err := m.termAt(m.last)
		if err != nil {
			return 0, err
		}
		if term < lastTerm {
			m.logf("input=append term=%d; output=ErrTerm; reason=lastTerm=%d", term, lastTerm)
			return 0, ErrTerm
		}
	}
	m.last++
	m.log = append(m.log, term)
	m.logf("input=append term=%d; output=index=%d; reason=monotonic term", term, m.last)
	return m.last, nil
}

func (m *naiveModel) commitIndex(idx int) error {
	if idx < m.commit || idx > m.last {
		m.logf("input=commit idx=%d; output=ErrRange; reason=commit=%d last=%d", idx, m.commit, m.last)
		return ErrRange
	}
	m.commit = idx
	m.logf("input=commit idx=%d; output=ok; reason=commit advanced", idx)
	return nil
}

func (m *naiveModel) apply(idx int) (bool, error) {
	if idx < m.applied || idx > m.commit {
		m.logf("input=apply idx=%d; output=ErrRange; reason=applied=%d commit=%d", idx, m.applied, m.commit)
		return false, ErrRange
	}
	m.applied = idx
	if idx-m.snapIndex < m.Thr {
		m.logf("input=apply idx=%d; output=snapshot=false; reason=delta=%d<thr=%d", idx, idx-m.snapIndex, m.Thr)
		return false, nil
	}
	if err := m.takeSnapshot("apply threshold reached"); err != nil {
		return false, err
	}
	return true, nil
}

func (m *naiveModel) snapshot() error {
	if m.applied <= m.snapIndex {
		m.logf("input=Snapshot; output=ErrNoProgress; reason=applied=%d snapIndex=%d", m.applied, m.snapIndex)
		return ErrNoProgress
	}
	return m.takeSnapshot("manual Snapshot")
}

func (m *naiveModel) takeSnapshot(reason string) error {
	term, err := m.termAt(m.applied)
	if err != nil {
		return err
	}
	m.snapIndex = m.applied
	m.snapTerm = term
	m.logf("snapshot fixed snapIndex=%d snapTerm=%d; reason=%s", m.snapIndex, m.snapTerm, reason)
	m.compact("snapshot")
	return nil
}

func (m *naiveModel) compact(reason string) {
	cut := m.snapIndex - m.T
	if cut < 0 {
		cut = 0
	}
	fixedBy := "retain"
	for _, p := range m.peers {
		if p.inFlight {
			if p.snapshot < cut {
				cut = p.snapshot
				fixedBy = "in-flight snapshot"
			}
			continue
		}
		if m.last-p.match <= m.L {
			if p.match < cut {
				cut = p.match
				fixedBy = "neighbor protection"
			}
		}
	}
	if cut <= m.baseIndex {
		m.logf("compact unchanged cut=%d base=%d; reason=%s", cut, m.baseIndex, reason)
		return
	}
	term, err := m.termAt(cut)
	if err != nil {
		panic(err)
	}
	count := cut - m.baseIndex
	m.log = m.log[count:]
	m.baseIndex = cut
	m.baseTerm = term
	m.removed += count
	m.logf("compact cut=%d baseTerm=%d removed+= %d; reason=%s fixedBy=%s", cut, term, count, reason, fixedBy)
}

func (m *naiveModel) addPeer(id string) error {
	if id == "" {
		m.logf("input=add id=%q; output=ErrParam; reason=empty id", id)
		return ErrParam
	}
	if _, ok := m.peers[id]; ok {
		m.logf("input=add id=%q; output=ErrExists; reason=peer exists", id)
		return ErrExists
	}
	m.peers[id] = &naivePeer{next: m.last + 1}
	m.logf("input=add id=%q; output=ok; reason=match=0 next=%d", id, m.last+1)
	return nil
}

func (m *naiveModel) peer(id string) (*naivePeer, string, error) {
	if id == "" {
		return nil, "empty id", ErrParam
	}
	p := m.peers[id]
	if p == nil {
		return nil, "unknown peer", ErrUnknownPeer
	}
	return p, "", nil
}

func (m *naiveModel) ack(id string, match int) error {
	p, reason, err := m.peer(id)
	if err != nil {
		m.logf("input=ack id=%q match=%d; output=%v; reason=%s", id, match, err, reason)
		return err
	}
	if match < p.match || match > m.last {
		m.logf("input=ack id=%q match=%d; output=ErrRange; reason=current=%d last=%d", id, match, p.match, m.last)
		return ErrRange
	}
	p.match = match
	p.next = match + 1
	m.logf("input=ack id=%q match=%d; output=ok; reason=Ack does not Compact", id, match)
	return nil
}

func (m *naiveModel) retreat(id string, next int) error {
	p, reason, err := m.peer(id)
	if err != nil {
		m.logf("input=retreat id=%q next=%d; output=%v; reason=%s", id, next, err, reason)
		return err
	}
	if next <= p.match || next > p.next {
		m.logf("input=retreat id=%q next=%d; output=ErrRange; reason=match=%d currentNext=%d", id, next, p.match, p.next)
		return ErrRange
	}
	p.next = next
	m.logf("input=retreat id=%q next=%d; output=ok; reason=strictly after match", id, next)
	return nil
}

func (m *naiveModel) dropPeer(id string) error {
	if _, reason, err := m.peer(id); err != nil {
		m.logf("input=drop id=%q; output=%v; reason=%s", id, err, reason)
		return err
	}
	delete(m.peers, id)
	m.compact("DropPeer")
	m.logf("input=drop id=%q; output=ok; reason=peer and fixpoint removed", id)
	return nil
}

func (m *naiveModel) plan(id string) (Plan, error) {
	p, reason, err := m.peer(id)
	if err != nil {
		m.logf("input=plan id=%q; output=%v; reason=%s", id, err, reason)
		return Plan{}, err
	}
	if p.inFlight {
		plan := Plan{Kind: PlanInstalling, SnapIndex: p.snapshot, SnapTerm: p.snapshotTerm}
		m.logf("input=plan id=%q; output=%+v; reason=in-flight takes precedence", id, plan)
		return plan, nil
	}
	if p.next <= m.baseIndex {
		plan := Plan{Kind: PlanNeedSnapshot, SnapIndex: m.snapIndex, SnapTerm: m.snapTerm}
		m.logf("input=plan id=%q; output=%+v; reason=next=%d<=base=%d", id, plan, p.next, m.baseIndex)
		return plan, nil
	}
	prev := p.next - 1
	term, err := m.termAt(prev)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Kind: PlanAppend, PrevIndex: prev, PrevTerm: term, From: p.next, To: m.last}
	m.logf("input=plan id=%q; output=%+v; reason=next=%d>base=%d", id, plan, p.next, m.baseIndex)
	return plan, nil
}

func (m *naiveModel) startSnapshot(id string) (int, int, error) {
	p, reason, err := m.peer(id)
	if err != nil {
		m.logf("input=start id=%q; output=%v; reason=%s", id, err, reason)
		return 0, 0, err
	}
	if p.inFlight {
		m.logf("input=start id=%q; output=ErrInFlight; reason=one snapshot at most", id)
		return 0, 0, ErrInFlight
	}
	plan, _ := m.plan(id)
	if plan.Kind != PlanNeedSnapshot {
		m.logf("input=start id=%q; output=ErrNotNeeded; reason=plan=%s", id, plan.Kind)
		return 0, 0, ErrNotNeeded
	}
	p.inFlight = true
	p.snapshot = m.snapIndex
	p.snapshotTerm = m.snapTerm
	m.logf("input=start id=%q; output=s=%d term=%d; reason=fixpoint recorded", id, p.snapshot, p.snapshotTerm)
	return p.snapshot, p.snapshotTerm, nil
}

func (m *naiveModel) finishSnapshot(id string) error {
	p, reason, err := m.peer(id)
	if err != nil {
		m.logf("input=finish id=%q; output=%v; reason=%s", id, err, reason)
		return err
	}
	if !p.inFlight {
		m.logf("input=finish id=%q; output=ErrNotInFlight; reason=no fixpoint", id)
		return ErrNotInFlight
	}
	s := p.snapshot
	p.inFlight = false
	p.snapshot = 0
	p.snapshotTerm = 0
	if s > p.match {
		p.match = s
	}
	p.next = p.match + 1
	m.compact("FinishSnapshot releases first")
	m.logf("input=finish id=%q; output=ok; reason=match=%d next=%d", id, p.match, p.next)
	return nil
}

func (m *naiveModel) abortSnapshot(id string) error {
	p, reason, err := m.peer(id)
	if err != nil {
		m.logf("input=abort id=%q; output=%v; reason=%s", id, err, reason)
		return err
	}
	if !p.inFlight {
		m.logf("input=abort id=%q; output=ErrNotInFlight; reason=no fixpoint", id)
		return ErrNotInFlight
	}
	p.inFlight = false
	p.snapshot = 0
	p.snapshotTerm = 0
	m.compact("AbortSnapshot releases first")
	m.logf("input=abort id=%q; output=ok; reason=match=%d next=%d unchanged", id, p.match, p.next)
	return nil
}

var opNames = []string{
	"append", "commit", "apply", "snapshot", "compact", "term",
	"add", "ack", "retreat", "drop", "plan", "start", "finish", "abort",
}

func TestRandomOperationsMatchNaiveModel(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed*7919)))
			T := rng.IntN(6)
			Thr := 1 + rng.IntN(5)
			L := rng.IntN(6)

			c, err := New(T, Thr, L)
			if err != nil {
				t.Fatal(err)
			}
			m := newNaive(T, Thr, L)
			m.logf("parameters T=%d Thr=%d L=%d; reason=initial empty log", T, Thr, L)

			for step := 0; step < 80; step++ {
				op := randomOperation(rng, m)
				runModelOperation(t, c, m, op)
				assertModelMatches(t, c, m, seed, step, op)
			}

			t.Logf("random trace seed=%d\n%s", seed, m.logs.String())
		})
	}
}

func randomOperation(rng *rand.Rand, m *naiveModel) randomOp {
	op := randomOp{name: opNames[rng.IntN(len(opNames))]}
	if needsPeer(op.name) {
		op.id = randomPeerID(rng, m)
	}

	switch op.name {
	case "append":
		op.arg = 1 + rng.IntN(4)
		if m.last > m.baseIndex {
			op.arg = m.log[len(m.log)-1] - 1 + rng.IntN(3)
			if op.arg < 1 {
				op.arg = 1
			}
		} else if m.last > 0 {
			op.arg = m.baseTerm - 1 + rng.IntN(3)
			if op.arg < 1 {
				op.arg = 1
			}
		}
	case "commit":
		op.arg = randomBetween(rng, m.commit, m.last+2)
	case "apply":
		op.arg = randomBetween(rng, m.applied, m.commit+2)
	case "term":
		op.arg = randomBetween(rng, 0, m.last+3)
	case "ack":
		if p := m.peers[op.id]; p != nil {
			op.arg = randomBetween(rng, p.match, m.last+2)
		} else {
			op.arg = rng.IntN(m.last + 2)
		}
	case "retreat":
		if p := m.peers[op.id]; p != nil {
			op.arg = randomBetween(rng, p.match, p.next+2)
		} else {
			op.arg = rng.IntN(m.last + 3)
		}
	}
	return op
}

func needsPeer(name string) bool {
	switch name {
	case "add", "ack", "retreat", "drop", "plan", "start", "finish", "abort":
		return true
	default:
		return false
	}
}

func randomPeerID(rng *rand.Rand, m *naiveModel) string {
	if len(m.peers) > 0 && rng.IntN(4) != 0 {
		ids := make([]string, 0, len(m.peers))
		for id := range m.peers {
			ids = append(ids, id)
		}
		return ids[rng.IntN(len(ids))]
	}
	if rng.IntN(12) == 0 {
		return ""
	}
	return fmt.Sprintf("p%d", rng.IntN(4))
}

func randomBetween(rng *rand.Rand, low, high int) int {
	if high <= low {
		return low
	}
	return low + rng.IntN(high-low)
}

func runModelOperation(t *testing.T, c *Coordinator, m *naiveModel, op randomOp) {
	t.Helper()
	switch op.name {
	case "append":
		got, gotErr := c.Append(op.arg)
		want, wantErr := m.appendLog(op.arg)
		compareModelValue(t, op, "index", got, want, gotErr, wantErr)
	case "commit":
		compareModelError(t, op, c.Commit(op.arg), m.commitIndex(op.arg))
	case "apply":
		got, gotErr := c.Apply(op.arg)
		want, wantErr := m.apply(op.arg)
		compareModelValue(t, op, "snapshotTaken", got, want, gotErr, wantErr)
	case "snapshot":
		compareModelError(t, op, c.Snapshot(), m.snapshot())
	case "compact":
		c.Compact()
		m.compact("explicit Compact")
		m.logf("input=%s; output=ok; reason=compaction evaluated", op)
	case "term":
		got, gotErr := c.TermAt(op.arg)
		want, wantErr := m.termAt(op.arg)
		compareModelValue(t, op, "term", got, want, gotErr, wantErr)
	case "add":
		compareModelError(t, op, c.AddPeer(op.id), m.addPeer(op.id))
	case "ack":
		compareModelError(t, op, c.Ack(op.id, op.arg), m.ack(op.id, op.arg))
	case "retreat":
		compareModelError(t, op, c.Retreat(op.id, op.arg), m.retreat(op.id, op.arg))
	case "drop":
		compareModelError(t, op, c.DropPeer(op.id), m.dropPeer(op.id))
	case "plan":
		got, gotErr := c.Plan(op.id)
		want, wantErr := m.plan(op.id)
		compareModelValue(t, op, "plan", got, want, gotErr, wantErr)
	case "start":
		s, term, gotErr := c.StartSnapshot(op.id)
		ws, wt, wantErr := m.startSnapshot(op.id)
		compareModelValue(t, op, "snapshotIndex", s, ws, gotErr, wantErr)
		if term != wt {
			t.Fatalf("%s snapshotTerm = %d, want %d", op, term, wt)
		}
	case "finish":
		compareModelError(t, op, c.FinishSnapshot(op.id), m.finishSnapshot(op.id))
	case "abort":
		compareModelError(t, op, c.AbortSnapshot(op.id), m.abortSnapshot(op.id))
	}
}

func compareModelValue[T comparable](t *testing.T, op randomOp, label string, got, want T, gotErr, wantErr error) {
	t.Helper()
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("%s error = %v, want %v", op, gotErr, wantErr)
	}
	if got != want {
		t.Fatalf("%s %s = %v, want %v", op, label, got, want)
	}
}

func compareModelError(t *testing.T, op randomOp, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s error = %v, want %v", op, got, want)
	}
}

func assertModelMatches(t *testing.T, c *Coordinator, m *naiveModel, seed int64, step int, op randomOp) {
	t.Helper()
	c.mu.Lock()
	gotState := []int{
		c.last, c.commit, c.applied, c.snapIndex, c.snapTerm,
		c.baseIndex, c.baseTerm, c.removed, len(c.entries),
	}
	gotPeers := map[string][5]int{}
	for id, p := range c.peers {
		inFlight := 0
		if p.hasSnapshot {
			inFlight = 1
		}
		gotPeers[id] = [5]int{p.match, p.next, p.snapshot, p.snapshotTerm, inFlight}
	}
	c.mu.Unlock()

	wantState := []int{
		m.last, m.commit, m.applied, m.snapIndex, m.snapTerm,
		m.baseIndex, m.baseTerm, m.removed, len(m.log),
	}
	wantPeers := map[string][5]int{}
	for id, p := range m.peers {
		inFlight := 0
		if p.inFlight {
			inFlight = 1
		}
		wantPeers[id] = [5]int{p.match, p.next, p.snapshot, p.snapshotTerm, inFlight}
	}

	if !reflect.DeepEqual(gotState, wantState) || !reflect.DeepEqual(gotPeers, wantPeers) {
		t.Fatalf("seed=%d step=%d op=%s\nstate got=%v want=%v\npeers got=%v want=%v\ntrace:\n%s",
			seed, step, op, gotState, wantState, gotPeers, wantPeers, m.logs.String())
	}
}

var _ = errors.Is
