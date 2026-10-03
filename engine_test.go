package ontology

import (
	"errors"
	"reflect"
	"sort"
	"testing"
)

func assertErrorIs(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func assertInts(t *testing.T, got, want []int) {
	t.Helper()
	if len(got) == 0 {
		got = []int{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ints = %v, want %v", got, want)
	}
}

func TestNewEngineRejectsInvalidKeyCount(t *testing.T) {
	for _, k := range []int{0, -1, 65, 100} {
		if _, err := NewEngine(k); !errors.Is(err, ErrInvalidKeyCount) {
			t.Fatalf("NewEngine(%d) error = %v, want ErrInvalidKeyCount", k, err)
		}
	}
}

func TestSpeculativeReadCommitOrder(t *testing.T) {
	e, err := NewEngine(1)
	if err != nil {
		t.Fatal(err)
	}

	t1 := e.Begin()
	t2 := e.Begin()
	if err := e.Write(t1, 0, 5); err != nil {
		t.Fatal(err)
	}
	if et := mustPrecommit(t, e, t1); et != 3 {
		t.Fatalf("T1 ET = %d, want 3", et)
	}

	value, err := e.Read(t2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if value != 0 {
		t.Fatalf("T2 read = %d, want 0", value)
	}
	if len(e.dependencies[t2]) != 0 {
		t.Fatalf("T2 dependencies = %v, want empty", e.dependencies[t2])
	}

	t3 := e.Begin()
	value, err = e.Read(t3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if value != 5 {
		t.Fatalf("T3 read = %d, want 5", value)
	}
	if !hasDependency(e, t3, t1) {
		t.Fatalf("T3 does not depend on T1: %v", e.dependencies[t3])
	}

	if et := mustPrecommit(t, e, t3); et != 5 {
		t.Fatalf("T3 ET = %d, want 5", et)
	}
	order, err := e.Finish(t3)
	if err != nil {
		t.Fatal(err)
	}
	assertInts(t, order, []int{})

	order, err = e.Finish(t1)
	if err != nil {
		t.Fatal(err)
	}
	assertInts(t, order, []int{1, 3})
}

func TestSpeculativeReadCascadeAbort(t *testing.T) {
	e, _ := NewEngine(1)
	t1 := e.Begin()
	t2 := e.Begin()
	_ = e.Write(t1, 0, 5)
	_ = mustPrecommit(t, e, t1)
	if _, err := e.Read(t2, 0); err != nil {
		t.Fatal(err)
	}
	t3 := e.Begin()
	if value, err := e.Read(t3, 0); err != nil || value != 5 {
		t.Fatalf("Read T3 = (%d, %v), want (5, nil)", value, err)
	}
	_ = mustPrecommit(t, e, t3)

	aborted, err := e.Abort(t1)
	if err != nil {
		t.Fatal(err)
	}
	assertInts(t, aborted, []int{1, 3})
	if e.transactions[t1].status != Aborted || e.transactions[t3].status != Aborted {
		t.Fatalf("statuses T1=%v T3=%v, want both aborted", e.transactions[t1].status, e.transactions[t3].status)
	}
}

func TestActiveCreatorInvisibleAndActiveEnderKeepsOldVersionVisible(t *testing.T) {
	e, _ := NewEngine(1)
	t1 := e.Begin()
	t2 := e.Begin()
	if err := e.Write(t1, 0, 7); err != nil {
		t.Fatal(err)
	}

	value, err := e.Read(t2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if value != 0 {
		t.Fatalf("read = %d, want old value 0", value)
	}
	if len(e.dependencies[t2]) != 0 {
		t.Fatalf("active creator created dependency: %v", e.dependencies[t2])
	}
}

func TestReadOwnWrite(t *testing.T) {
	e, _ := NewEngine(1)
	t1 := e.Begin()
	if err := e.Write(t1, 0, 9); err != nil {
		t.Fatal(err)
	}
	value, err := e.Read(t1, 0)
	if err != nil || value != 9 {
		t.Fatalf("Read own write = (%d, %v), want (9, nil)", value, err)
	}
}

func TestWriteToPreparedVersionAddsDependencyAndCommitsInOrder(t *testing.T) {
	e, _ := NewEngine(1)
	t1 := e.Begin()
	if err := e.Write(t1, 0, 5); err != nil {
		t.Fatal(err)
	}
	_ = mustPrecommit(t, e, t1)

	t2 := e.Begin()
	if err := e.Write(t2, 0, 6); err != nil {
		t.Fatal(err)
	}
	if !hasDependency(e, t2, t1) {
		t.Fatalf("T2 dependencies = %v, want dependency on T1", e.dependencies[t2])
	}
	_ = mustPrecommit(t, e, t2)
	order2, err := e.Finish(t2)
	if err != nil {
		t.Fatal(err)
	}
	assertInts(t, order2, []int{})
	order1, err := e.Finish(t1)
	if err != nil {
		t.Fatal(err)
	}
	assertInts(t, order1, []int{1, 2})
}

func TestWriteToLaterPreparedVersionConflicts(t *testing.T) {
	e, _ := NewEngine(1)
	t1 := e.Begin()
	t2 := e.Begin()
	_ = e.Write(t1, 0, 5)
	_ = mustPrecommit(t, e, t1)

	err := e.Write(t2, 0, 6)
	assertErrorIs(t, err, ErrWriteConflict)
	if e.transactions[t2].status != Aborted {
		t.Fatalf("T2 status = %v, want aborted", e.transactions[t2].status)
	}
}

func TestWriteToVersionWithEffectiveEnderConflicts(t *testing.T) {
	e, _ := NewEngine(1)
	t1 := e.Begin()
	t2 := e.Begin()
	e.clock++
	e.transactions[t1].status = Prepared
	e.transactions[t1].endTime = e.clock
	e.versions[0][0].ender = t1

	err := e.Write(t2, 0, 9)
	assertErrorIs(t, err, ErrWriteConflict)
	if e.transactions[t2].status != Aborted {
		t.Fatalf("T2 status = %v, want aborted", e.transactions[t2].status)
	}
}

func TestGarbageVersionAndAbortedEnderMarkerAreIgnored(t *testing.T) {
	e, _ := NewEngine(1)
	t1 := e.Begin()
	t2 := e.Begin()
	t3 := e.Begin()
	_ = e.Write(t1, 0, 5)
	aborted, err := e.Abort(t1)
	if err != nil {
		t.Fatal(err)
	}
	assertInts(t, aborted, []int{1})
	e.versions[0][len(e.versions[0])-1].ender = t2
	e.transactions[t2].status = Aborted

	value, err := e.Read(t3, 0)
	if err != nil || value != 0 {
		t.Fatalf("Read after garbage = (%d, %v), want (0, nil)", value, err)
	}
	if err := e.Write(t3, 0, 8); err != nil {
		t.Fatalf("Write after aborted ender marker: %v", err)
	}
}

func TestMultiLayerDependencyCommitOrder(t *testing.T) {
	e, _ := NewEngine(1)
	t1 := e.Begin()
	_ = e.Write(t1, 0, 1)
	et1 := mustPrecommit(t, e, t1)

	t2 := e.Begin()
	value, err := e.Read(t2, 0)
	if err != nil || value != 1 {
		t.Fatalf("T2 read = (%d, %v), want 1", value, err)
	}
	_ = e.Write(t2, 0, 2)
	et2 := mustPrecommit(t, e, t2)

	t3 := e.Begin()
	value, err = e.Read(t3, 0)
	if err != nil || value != 2 {
		t.Fatalf("T3 read = (%d, %v), want 2", value, err)
	}
	_ = e.Write(t3, 0, 3)
	_ = mustPrecommit(t, e, t3)

	if et1 >= et2 {
		t.Fatalf("ET ordering invalid: %d >= %d", et1, et2)
	}
	assertInts(t, mustFinish(t, e, t3), []int{})
	assertInts(t, mustFinish(t, e, t2), []int{})
	assertInts(t, mustFinish(t, e, t1), []int{1, 2, 3})
}

func TestMultiLayerCascadeIncludesActiveDependents(t *testing.T) {
	e, _ := NewEngine(1)
	t1 := e.Begin()
	_ = e.Write(t1, 0, 1)
	_ = mustPrecommit(t, e, t1)

	t2 := e.Begin()
	if _, err := e.Read(t2, 0); err != nil {
		t.Fatal(err)
	}
	_ = e.Write(t2, 0, 2)
	_ = mustPrecommit(t, e, t2)

	t3 := e.Begin()
	if _, err := e.Read(t3, 0); err != nil {
		t.Fatal(err)
	}
	t4 := e.Begin()
	_ = e.Write(t4, 0, 4)
	if !hasDependency(e, t4, t2) {
		t.Fatalf("T4 dependencies = %v, want T2", e.dependencies[t4])
	}

	aborted, err := e.Abort(t1)
	if err != nil {
		t.Fatal(err)
	}
	assertInts(t, aborted, []int{1, 2, 3, 4})
}

func TestRejectionOrderAndNoStateChange(t *testing.T) {
	e, _ := NewEngine(1)
	t1 := e.Begin()
	_ = e.Write(t1, 0, 1)
	_ = mustPrecommit(t, e, t1)
	assertInts(t, mustFinish(t, e, t1), []int{1})

	before := snapshotEngine(e)
	_, err := e.Read(999, -1)
	assertErrorIs(t, err, ErrUnknownTx)
	_, err = e.Read(t1, -1)
	assertErrorIs(t, err, ErrInvalidStatus)

	t2 := e.Begin()
	_, err = e.Read(t2, -1)
	assertErrorIs(t, err, ErrInvalidKey)
	_, err = e.Finish(t2)
	assertErrorIs(t, err, ErrInvalidStatus)
	_, err = e.Abort(t1)
	assertErrorIs(t, err, ErrInvalidStatus)

	e.transactions[t2].status = Aborted
	after := snapshotEngine(e)
	before.tx[t2] = txSnapshot{readTime: 3, status: Aborted}
	before.clock = after.clock
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("rejected calls changed state:\nbefore=%#v\nafter =%#v", before, after)
	}
}

func mustPrecommit(t *testing.T, e *Engine, txID int) int {
	t.Helper()
	et, err := e.Precommit(txID)
	if err != nil {
		t.Fatalf("Precommit(%d): %v", txID, err)
	}
	return et
}

func mustFinish(t *testing.T, e *Engine, txID int) []int {
	t.Helper()
	order, err := e.Finish(txID)
	if err != nil {
		t.Fatalf("Finish(%d): %v", txID, err)
	}
	return order
}

func hasDependency(e *Engine, dependent, dependency int) bool {
	_, ok := e.dependencies[dependent][dependency]
	return ok
}

type txSnapshot struct {
	readTime int
	endTime  int
	status   TxStatus
	finished bool
}

type versionSnapshot struct {
	value   int
	creator int
	ender   int
}

type engineSnapshot struct {
	clock  int
	tx     map[int]txSnapshot
	keys   [][]versionSnapshot
	deps   map[int][]int
	depend map[int][]int
}

func snapshotEngine(e *Engine) engineSnapshot {
	s := engineSnapshot{
		clock:  e.clock,
		tx:     make(map[int]txSnapshot),
		keys:   make([][]versionSnapshot, e.keyCount),
		deps:   make(map[int][]int),
		depend: make(map[int][]int),
	}
	for id, tx := range e.transactions {
		s.tx[id] = txSnapshot{
			readTime: tx.readTime,
			endTime:  tx.endTime,
			status:   tx.status,
			finished: tx.finished,
		}
	}
	for key, versions := range e.versions {
		for _, v := range versions {
			s.keys[key] = append(s.keys[key], versionSnapshot{
				value:   v.value,
				creator: v.creator,
				ender:   v.ender,
			})
		}
	}
	for id, values := range e.dependencies {
		for value := range values {
			s.deps[id] = append(s.deps[id], value)
		}
		sort.Ints(s.deps[id])
	}
	for id, values := range e.dependents {
		for value := range values {
			s.depend[id] = append(s.depend[id], value)
		}
		sort.Ints(s.depend[id])
	}
	return s
}
