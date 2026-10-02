package coordinator

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

type modelPlanKind int

const (
	modelAppendEntries modelPlanKind = iota
	modelInstalling
	modelNeedSnapshot
)

type modelPlan struct {
	kind      modelPlanKind
	snapshot  int
	snapTerm  int
	prevIndex int
	prevTerm  int
	from      int
	to        int
}

type modelPeer struct {
	match        int
	next         int
	snapshot     int
	snapshotTerm int
	hasSnapshot  bool
}

type modelCoordinator struct {
	tail      int
	threshold int
	lag       int
	entries   map[int]int
	last      int
	commit    int
	applied   int
	snapIndex int
	snapTerm  int
	baseIndex int
	baseTerm  int
	removed   int
	peers     map[string]*modelPeer
}

type modelCoordinatorOrError struct {
	value *modelCoordinator
	err   error
}

func modelNew(tail int, threshold int, lag int) (*modelCoordinator, error) {
	if tail < 0 || threshold < 1 || lag < 0 {
		return nil, ErrParam
	}
	return &modelCoordinator{
		tail:      tail,
		threshold: threshold,
		lag:       lag,
		entries:   make(map[int]int),
		peers:     make(map[string]*modelPeer),
	}, nil
}

func (m *modelCoordinator) appendEntry(term int) (int, error) {
	if term < 1 {
		return 0, ErrParam
	}
	if m.last > 0 && term < m.entries[m.last] {
		return 0, ErrTerm
	}
	m.last++
	m.entries[m.last] = term
	return m.last, nil
}

func (m *modelCoordinator) commitIndex(idx int) error {
	if idx < m.commit || idx > m.last {
		return ErrRange
	}
	m.commit = idx
	return nil
}

func (m *modelCoordinator) applyIndex(idx int) (bool, error) {
	if idx < m.applied || idx > m.commit {
		return false, ErrRange
	}
	m.applied = idx
	if idx-m.snapIndex < m.threshold {
		return false, nil
	}
	m.takeSnapshot()
	return true, nil
}

func (m *modelCoordinator) manualSnapshot() error {
	if m.applied <= m.snapIndex {
		return ErrNoProgress
	}
	m.takeSnapshot()
	return nil
}

func (m *modelCoordinator) takeSnapshot() {
	m.snapIndex = m.applied
	m.snapTerm = m.entries[m.applied]
	m.compact()
}

func (m *modelCoordinator) termAt(idx int) (int, error) {
	if idx == m.baseIndex {
		return m.baseTerm, nil
	}
	if idx < m.baseIndex {
		return 0, ErrCompacted
	}
	if idx > m.last {
		return 0, ErrRange
	}
	return m.entries[idx], nil
}

func (m *modelCoordinator) compact() {
	cut := m.snapIndex - m.tail
	if cut < 0 {
		cut = 0
	}
	for _, follower := range m.peers {
		if follower.hasSnapshot {
			if follower.snapshot < cut {
				cut = follower.snapshot
			}
			continue
		}
		if m.last-follower.match <= m.lag && follower.match < cut {
			cut = follower.match
		}
	}
	if cut <= m.baseIndex {
		return
	}
	m.baseTerm = m.entries[cut]
	for idx := m.baseIndex + 1; idx <= cut-1; idx++ {
		delete(m.entries, idx)
	}
	m.removed += cut - m.baseIndex
	m.baseIndex = cut
}

func (m *modelCoordinator) addPeer(id string) error {
	if id == "" {
		return ErrParam
	}
	if _, ok := m.peers[id]; ok {
		return ErrExists
	}
	m.peers[id] = &modelPeer{next: m.last + 1}
	return nil
}

func (m *modelCoordinator) ack(id string, match int) error {
	if id == "" {
		return ErrParam
	}
	follower, ok := m.peers[id]
	if !ok {
		return ErrUnknownPeer
	}
	if match < follower.match || match > m.last {
		return ErrRange
	}
	follower.match = match
	follower.next = match + 1
	return nil
}

func (m *modelCoordinator) retreat(id string, next int) error {
	if id == "" {
		return ErrParam
	}
	follower, ok := m.peers[id]
	if !ok {
		return ErrUnknownPeer
	}
	if next <= follower.match || next > follower.next {
		return ErrRange
	}
	follower.next = next
	return nil
}

func (m *modelCoordinator) dropPeer(id string) error {
	if id == "" {
		return ErrParam
	}
	if _, ok := m.peers[id]; !ok {
		return ErrUnknownPeer
	}
	delete(m.peers, id)
	m.compact()
	return nil
}

func (m *modelCoordinator) plan(id string) (modelPlan, error) {
	if id == "" {
		return modelPlan{}, ErrParam
	}
	follower, ok := m.peers[id]
	if !ok {
		return modelPlan{}, ErrUnknownPeer
	}
	if follower.hasSnapshot {
		return modelPlan{
			kind:     modelInstalling,
			snapshot: follower.snapshot,
			snapTerm: follower.snapshotTerm,
		}, nil
	}
	if follower.next <= m.baseIndex {
		return modelPlan{
			kind:     modelNeedSnapshot,
			snapshot: m.snapIndex,
			snapTerm: m.snapTerm,
		}, nil
	}
	prevTerm, err := m.termAt(follower.next - 1)
	if err != nil {
		panic(err)
	}
	return modelPlan{
		kind:      modelAppendEntries,
		prevIndex: follower.next - 1,
		prevTerm:  prevTerm,
		from:      follower.next,
		to:        m.last,
	}, nil
}

func (m *modelCoordinator) startSnapshot(id string) (int, int, error) {
	if id == "" {
		return 0, 0, ErrParam
	}
	follower, ok := m.peers[id]
	if !ok {
		return 0, 0, ErrUnknownPeer
	}
	if follower.hasSnapshot {
		return 0, 0, ErrInFlight
	}
	plan, _ := m.plan(id)
	if plan.kind != modelNeedSnapshot {
		return 0, 0, ErrNotNeeded
	}
	follower.hasSnapshot = true
	follower.snapshot = m.snapIndex
	follower.snapshotTerm = m.snapTerm
	return follower.snapshot, follower.snapshotTerm, nil
}

func (m *modelCoordinator) finishSnapshot(id string) error {
	if id == "" {
		return ErrParam
	}
	follower, ok := m.peers[id]
	if !ok {
		return ErrUnknownPeer
	}
	if !follower.hasSnapshot {
		return ErrNotInFlight
	}
	snapshot := follower.snapshot
	follower.hasSnapshot = false
	follower.snapshot = 0
	follower.snapshotTerm = 0
	if snapshot > follower.match {
		follower.match = snapshot
	}
	follower.next = follower.match + 1
	m.compact()
	return nil
}

func (m *modelCoordinator) abortSnapshot(id string) error {
	if id == "" {
		return ErrParam
	}
	follower, ok := m.peers[id]
	if !ok {
		return ErrUnknownPeer
	}
	if !follower.hasSnapshot {
		return ErrNotInFlight
	}
	follower.hasSnapshot = false
	follower.snapshot = 0
	follower.snapshotTerm = 0
	m.compact()
	return nil
}

func TestRandomCompareSkeleton(t *testing.T) {
	c, err := modelNew(0, 1, 0)
	if c == nil || err != nil {
		t.Fatalf("unexpected modelNew result")
	}
}

type comparison struct {
	name string
	args string
	got  string
	want string
}

func compareStates(t *testing.T, c *Coordinator, m *modelCoordinator, history []comparison) {
	t.Helper()
	if !(c.baseIndex <= c.snapIndex && c.snapIndex <= c.applied &&
		c.applied <= c.commit && c.commit <= c.last) {
		dumpAndFail(t, c, m, history, "invariant baseIndex<=snapIndex<=applied<=commit<=last failed")
	}
	if c.last != m.last || c.commit != m.commit || c.applied != m.applied ||
		c.snapIndex != m.snapIndex || c.snapTerm != m.snapTerm ||
		c.baseIndex != m.baseIndex || c.baseTerm != m.baseTerm || c.removed != m.removed {
		dumpAndFail(t, c, m, history, "leader state mismatch")
	}
	modelStored := 0
	for idx := m.baseIndex + 1; idx <= m.last; idx++ {
		if _, ok := m.entries[idx]; ok {
			modelStored++
		}
	}
	if len(c.entries) != modelStored {
		dumpAndFail(t, c, m, history, "stored entry count mismatch")
	}
	for idx := m.baseIndex + 1; idx <= m.last; idx++ {
		if c.entries[idx-c.baseIndex-1] != m.entries[idx] {
			dumpAndFail(t, c, m, history, fmt.Sprintf("term at %d mismatch", idx))
		}
	}
	if len(c.peers) != len(m.peers) {
		dumpAndFail(t, c, m, history, "peer count mismatch")
	}
	for id, actual := range c.peers {
		expected, ok := m.peers[id]
		if !ok {
			dumpAndFail(t, c, m, history, fmt.Sprintf("unexpected peer %q", id))
		}
		if actual.match != expected.match || actual.next != expected.next ||
			actual.hasSnapshot != expected.hasSnapshot ||
			actual.snapshot != expected.snapshot || actual.snapshotTerm != expected.snapshotTerm {
			dumpAndFail(t, c, m, history, fmt.Sprintf("peer %q mismatch", id))
		}
		if actual.hasSnapshot && actual.snapshot < actual.match {
			dumpAndFail(t, c, m, history, "in-flight snapshot below match")
		}
	}
}

func dumpAndFail(t *testing.T, c *Coordinator, m *modelCoordinator, history []comparison, reason string) {
	t.Helper()
	for _, item := range history {
		t.Logf("input=%s(%s) output got=%s want=%s", item.name, item.args, item.got, item.want)
	}
	t.Fatalf("%s\nactual={last:%d commit:%d applied:%d snap:(%d,%d) base:(%d,%d) removed:%d peers:%v}\nmodel={last:%d commit:%d applied:%d snap:(%d,%d) base:(%d,%d) removed:%d peers:%v}",
		reason,
		c.last, c.commit, c.applied, c.snapIndex, c.snapTerm, c.baseIndex, c.baseTerm, c.removed, c.peers,
		m.last, m.commit, m.applied, m.snapIndex, m.snapTerm, m.baseIndex, m.baseTerm, m.removed, m.peers)
}

func sameError(got error, want error) bool {
	return errors.Is(got, want)
}

func TestRandomCompare2000(t *testing.T) {
	const sequences = 2000
	const operations = 90
	const peerCount = 3

	for seed := int64(1); seed <= sequences; seed++ {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			tail := rng.Intn(6)
			threshold := 1 + rng.Intn(6)
			lag := rng.Intn(6)
			actual, err := New(tail, threshold, lag)
			if err != nil {
				t.Fatalf("New(%d,%d,%d) error = %v", tail, threshold, lag, err)
			}
			model, err := modelNew(tail, threshold, lag)
			if err != nil {
				t.Fatalf("modelNew(%d,%d,%d) error = %v", tail, threshold, lag, err)
			}

			ids := make([]string, peerCount)
			for i := range ids {
				ids[i] = fmt.Sprintf("p%d", i)
			}
			history := make([]comparison, 0, operations)
			logEntry := func(name string, args string, got string, want string, rule string) {
				history = append(history, comparison{
					name: name,
					args: args,
					got:  got + " basis=" + rule,
					want: want,
				})
				t.Logf("input=%s(%s) output got=%s want=%s basis=%s", name, args, got, want, rule)
			}

			for step := 0; step < operations; step++ {
				id := ids[rng.Intn(len(ids))]
				switch rng.Intn(15) {
				case 0:
					term := 1 + rng.Intn(4)
					if rng.Intn(6) == 0 {
						term = rng.Intn(2) - 1
					}
					gotIndex, gotErr := actual.Append(term)
					wantIndex, wantErr := model.appendEntry(term)
					logEntry("Append", fmt.Sprintf("term=%d", term),
						fmt.Sprintf("idx=%d err=%v", gotIndex, gotErr),
						fmt.Sprintf("idx=%d err=%v", wantIndex, wantErr),
						"term>=1 and nondecreasing")
					if gotIndex != wantIndex || !sameError(gotErr, wantErr) {
						dumpAndFail(t, actual, model, history, "Append mismatch")
					}
				case 1:
					idx := randomIndex(rng, model.last)
					gotErr := actual.Commit(idx)
					wantErr := model.commitIndex(idx)
					logEntry("Commit", fmt.Sprintf("idx=%d", idx),
						fmt.Sprintf("err=%v", gotErr), fmt.Sprintf("err=%v", wantErr),
						"commit<=idx<=last")
					if !sameError(gotErr, wantErr) {
						dumpAndFail(t, actual, model, history, "Commit mismatch")
					}
				case 2:
					idx := randomIndex(rng, model.commit)
					gotTook, gotErr := actual.Apply(idx)
					wantTook, wantErr := model.applyIndex(idx)
					logEntry("Apply", fmt.Sprintf("idx=%d", idx),
						fmt.Sprintf("took=%v err=%v", gotTook, gotErr),
						fmt.Sprintf("took=%v err=%v", wantTook, wantErr),
						fmt.Sprintf("idx-snapIndex>=%d", threshold))
					if gotTook != wantTook || !sameError(gotErr, wantErr) {
						dumpAndFail(t, actual, model, history, "Apply mismatch")
					}
				case 3:
					gotErr := actual.Snapshot()
					wantErr := model.manualSnapshot()
					logEntry("Snapshot", "",
						fmt.Sprintf("err=%v", gotErr), fmt.Sprintf("err=%v", wantErr),
						"applied>snapIndex")
					if !sameError(gotErr, wantErr) {
						dumpAndFail(t, actual, model, history, "Snapshot mismatch")
					}
				case 4:
					actual.Compact()
					model.compact()
					logEntry("Compact", "", "ok", "ok", "recompute cut from all fix points")
				case 5:
					idx := randomIndex(rng, model.last+1)
					gotTerm, gotErr := actual.TermAt(idx)
					wantTerm, wantErr := model.termAt(idx)
					logEntry("TermAt", fmt.Sprintf("idx=%d", idx),
						fmt.Sprintf("term=%d err=%v", gotTerm, gotErr),
						fmt.Sprintf("term=%d err=%v", wantTerm, wantErr),
						"baseIndex, stored range, last")
					if gotTerm != wantTerm || !sameError(gotErr, wantErr) {
						dumpAndFail(t, actual, model, history, "TermAt mismatch")
					}
				case 6:
					gotErr := actual.AddPeer(id)
					wantErr := model.addPeer(id)
					logEntry("AddPeer", fmt.Sprintf("id=%s", id),
						fmt.Sprintf("err=%v", gotErr), fmt.Sprintf("err=%v", wantErr),
						"match=0,next=last+1")
					if !sameError(gotErr, wantErr) {
						dumpAndFail(t, actual, model, history, "AddPeer mismatch")
					}
				case 7:
					match := randomIndex(rng, model.last)
					gotErr := actual.Ack(id, match)
					wantErr := model.ack(id, match)
					logEntry("Ack", fmt.Sprintf("id=%s,match=%d", id, match),
						fmt.Sprintf("err=%v", gotErr), fmt.Sprintf("err=%v", wantErr),
						"monotonic match<=last; no Compact")
					if !sameError(gotErr, wantErr) {
						dumpAndFail(t, actual, model, history, "Ack mismatch")
					}
				case 8:
					next := randomIndex(rng, model.last+2)
					gotErr := actual.Retreat(id, next)
					wantErr := model.retreat(id, next)
					logEntry("Retreat", fmt.Sprintf("id=%s,next=%d", id, next),
						fmt.Sprintf("err=%v", gotErr), fmt.Sprintf("err=%v", wantErr),
						"match<next<=oldNext")
					if !sameError(gotErr, wantErr) {
						dumpAndFail(t, actual, model, history, "Retreat mismatch")
					}
				case 9:
					gotErr := actual.DropPeer(id)
					wantErr := model.dropPeer(id)
					logEntry("DropPeer", fmt.Sprintf("id=%s", id),
						fmt.Sprintf("err=%v", gotErr), fmt.Sprintf("err=%v", wantErr),
						"remove peer, release fix point, then Compact")
					if !sameError(gotErr, wantErr) {
						dumpAndFail(t, actual, model, history, "DropPeer mismatch")
					}
				case 10:
					gotPlan, gotErr := actual.Plan(id)
					wantPlan, wantErr := model.plan(id)
					logEntry("Plan", fmt.Sprintf("id=%s", id),
						fmt.Sprintf("plan=%+v err=%v", gotPlan, gotErr),
						fmt.Sprintf("plan=%+v err=%v", wantPlan, wantErr),
						"Installing > NeedSnapshot when next<=baseIndex > AppendEntries")
					if !sameError(gotErr, wantErr) || planEqual(gotPlan, wantPlan) == false {
						dumpAndFail(t, actual, model, history, "Plan mismatch")
					}
				case 11:
					gotIndex, gotTerm, gotErr := actual.StartSnapshot(id)
					wantIndex, wantTerm, wantErr := model.startSnapshot(id)
					logEntry("StartSnapshot", fmt.Sprintf("id=%s", id),
						fmt.Sprintf("s=%d,term=%d,err=%v", gotIndex, gotTerm, gotErr),
						fmt.Sprintf("s=%d,term=%d,err=%v", wantIndex, wantTerm, wantErr),
						"unknown, in-flight, then NeedSnapshot")
					if gotIndex != wantIndex || gotTerm != wantTerm || !sameError(gotErr, wantErr) {
						dumpAndFail(t, actual, model, history, "StartSnapshot mismatch")
					}
				case 12:
					gotErr := actual.FinishSnapshot(id)
					wantErr := model.finishSnapshot(id)
					logEntry("FinishSnapshot", fmt.Sprintf("id=%s", id),
						fmt.Sprintf("err=%v", gotErr), fmt.Sprintf("err=%v", wantErr),
						"release and advance match before Compact")
					if !sameError(gotErr, wantErr) {
						dumpAndFail(t, actual, model, history, "FinishSnapshot mismatch")
					}
				case 13:
					gotErr := actual.AbortSnapshot(id)
					wantErr := model.abortSnapshot(id)
					logEntry("AbortSnapshot", fmt.Sprintf("id=%s", id),
						fmt.Sprintf("err=%v", gotErr), fmt.Sprintf("err=%v", wantErr),
						"release fix point then Compact; match/next unchanged")
					if !sameError(gotErr, wantErr) {
						dumpAndFail(t, actual, model, history, "AbortSnapshot mismatch")
					}
				case 14:
					if step%12 == 0 {
						actual.Compact()
						model.compact()
						logEntry("Compact", "", "ok", "ok", "periodic compact")
					}
				}
				compareStates(t, actual, model, history)
			}
		})
	}
}

func randomIndex(rng *rand.Rand, inclusiveMax int) int {
	if rng.Intn(7) == 0 {
		return -rng.Intn(3)
	}
	if inclusiveMax < 0 {
		return rng.Intn(3)
	}
	return rng.Intn(inclusiveMax+3) - 1
}

func planEqual(got Plan, want modelPlan) bool {
	wantKind := []PlanKind{AppendEntries, Installing, NeedSnapshot}[want.kind]
	return got.Kind == wantKind && got.Snapshot == want.snapshot && got.SnapTerm == want.snapTerm &&
		got.PrevIndex == want.prevIndex && got.PrevTerm == want.prevTerm &&
		got.From == want.from && got.To == want.to
}

func TestConcurrentOperations(t *testing.T) {
	c, err := New(1, 2, 2)
	if err != nil {
		t.Fatalf("New error = %v", err)
	}

	const workers = 8
	var wg sync.WaitGroup
	for worker := range workers {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			id := fmt.Sprintf("p%d", worker%4)
			for i := range 30 {
				term := 1 + (worker+i)%4
				if _, err := c.Append(term); err != nil && !errors.Is(err, ErrTerm) {
					t.Errorf("Append(%d) error = %v", term, err)
					return
				}
				if _, err := c.Apply(0); err != nil && !errors.Is(err, ErrRange) {
					t.Errorf("Apply error = %v", err)
					return
				}
				_ = c.AddPeer(id)
				_, _ = c.Plan(id)
				if _, _, err := c.StartSnapshot(id); err != nil &&
					!errors.Is(err, ErrInFlight) && !errors.Is(err, ErrNotNeeded) {
					t.Errorf("StartSnapshot error = %v", err)
					return
				}
				if i%2 == 0 {
					if err := c.AbortSnapshot(id); err != nil && !errors.Is(err, ErrNotInFlight) {
						t.Errorf("AbortSnapshot error = %v", err)
						return
					}
				} else {
					if err := c.FinishSnapshot(id); err != nil && !errors.Is(err, ErrNotInFlight) {
						t.Errorf("FinishSnapshot error = %v", err)
						return
					}
				}
			}
		}(worker)
	}
	wg.Wait()
}
