package ontology

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func pu(u uint64) *uint64 { return &u }

func b(s string) []byte { return []byte(s) }

func mustApply(t *testing.T, m *Manager, e Edit) {
	t.Helper()
	if err := m.Apply(e); err != nil {
		t.Fatalf("Apply(%+v) unexpected error: %v", e, err)
	}
}

func checkCorrupt(t *testing.T, err error, index int, cause error) {
	t.Helper()
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}
	if cause != nil && !errors.Is(err, cause) {
		t.Fatalf("err = %v, want cause %v", err, cause)
	}
	var re *RecoverError
	if !errors.As(err, &re) || re.Index != index {
		t.Fatalf("err = %v, want RecoverError index %d", err, index)
	}
}

func versionsEqual(a, z Version) bool {
	if a.LogNumber != z.LogNumber || a.NextFile != z.NextFile || a.LastSeq != z.LastSeq {
		return false
	}
	for l := 0; l < NumLevels; l++ {
		if len(a.Files[l]) != len(z.Files[l]) {
			return false
		}
		for i := range a.Files[l] {
			x, y := a.Files[l][i], z.Files[l][i]
			if x.Level != y.Level || x.Num != y.Num ||
				!bytes.Equal(x.Smallest, y.Smallest) || !bytes.Equal(x.Largest, y.Largest) {
				return false
			}
		}
	}
	return true
}

func TestNewInitialState(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrParam) {
		t.Fatalf("New(0) err = %v, want ErrParam", err)
	}
	m, err := New(3)
	if err != nil {
		t.Fatal(err)
	}
	v := m.View()
	if v.LogNumber != 0 || v.NextFile != 2 || v.LastSeq != 0 {
		t.Fatalf("initial pointers = %+v", v)
	}
	d := m.Disk()
	if d.Current != 1 || len(d.Manifests[1]) != 1 {
		t.Fatalf("initial disk = %+v", d)
	}
	if !d.Manifests[1][0].IsSnapshot || d.Manifests[1][0].Snapshot.NextFile != 2 {
		t.Fatalf("initial record not snapshot(2): %+v", d.Manifests[1][0])
	}
	rv, err := Recover(d)
	if err != nil || !versionsEqual(rv, v) {
		t.Fatalf("initial recover = %+v, %v", rv, err)
	}
}

func TestParamChecks(t *testing.T) {
	m, _ := New(3)
	cases := []Edit{
		{},
		{Adds: []File{{Level: 7, Num: 1, Smallest: b("a"), Largest: b("b")}}},
		{Adds: []File{{Level: -1, Num: 1, Smallest: b("a"), Largest: b("b")}}},
		{Adds: []File{{Level: 0, Num: 0, Smallest: b("a"), Largest: b("b")}}},
		{Adds: []File{{Level: 0, Num: 1, Smallest: nil, Largest: b("b")}}},
		{Adds: []File{{Level: 0, Num: 1, Smallest: b("a"), Largest: nil}}},
		{Adds: []File{{Level: 0, Num: 1, Smallest: b("z"), Largest: b("a")}}},
		{Dels: []Del{{Level: 9, Num: 1}}},
		{Dels: []Del{{Level: 0, Num: 0}}},
	}
	for i, e := range cases {
		if err := m.Apply(e); !errors.Is(err, ErrParam) {
			t.Fatalf("case %d: err = %v, want ErrParam", i, err)
		}
	}
}

func TestTouchingIntervalsOverlap(t *testing.T) {
	m, _ := New(10)
	mustApply(t, m, Edit{Adds: []File{{Level: 1, Num: 2, Smallest: b("a"), Largest: b("m")}}})
	err := m.Apply(Edit{Adds: []File{{Level: 1, Num: 3, Smallest: b("m"), Largest: b("z")}}})
	if !errors.Is(err, ErrOverlap) {
		t.Fatalf("touching intervals: err = %v, want ErrOverlap", err)
	}
	mustApply(t, m, Edit{Adds: []File{{Level: 1, Num: 3, Smallest: b("n"), Largest: b("z")}}})
	if len(m.Disk().Manifests[1]) != 3 {
		t.Fatalf("rejected edit must not append a record")
	}
	if p := m.OverlapProbes(); p > 2 {
		t.Fatalf("overlapProbes = %d > 2", p)
	}
}

func TestSameEditDeleteThenAddSameNum(t *testing.T) {
	m, _ := New(10)
	mustApply(t, m, Edit{Adds: []File{
		{Level: 0, Num: 5, Smallest: b("a"), Largest: b("c")},
	}})
	mustApply(t, m, Edit{
		Dels: []Del{{Level: 0, Num: 5}},
		Adds: []File{{Level: 1, Num: 5, Smallest: b("a"), Largest: b("c")}},
	})
	err := m.Apply(Edit{
		Adds: []File{
			{Level: 0, Num: 9, Smallest: b("x"), Largest: b("y")},
			{Level: 0, Num: 9, Smallest: b("p"), Largest: b("q")},
		},
	})
	if !errors.Is(err, ErrDupFile) {
		t.Fatalf("dup add: err = %v, want ErrDupFile", err)
	}
	// Deletes are always processed first, so deleting and re-adding the very
	// same number inside one edit is legal; the add-before-del failure instead
	// happens when a new edit tries to add a number that is still live without
	// deleting it first.
	mustApply(t, m, Edit{Adds: []File{{Level: 0, Num: 6, Smallest: b("d"), Largest: b("e")}}})
	err = m.Apply(Edit{
		Adds: []File{{Level: 0, Num: 6, Smallest: b("x"), Largest: b("y")}},
		Dels: []Del{{Level: 0, Num: 6}},
	})
	if err != nil {
		t.Fatalf("delete then add same num should succeed, got %v", err)
	}
	err = m.Apply(Edit{Adds: []File{{Level: 0, Num: 6, Smallest: b("p"), Largest: b("q")}}})
	if !errors.Is(err, ErrDupFile) {
		t.Fatalf("add existing num without prior delete: err = %v, want ErrDupFile", err)
	}
}

func TestErrorOrdering(t *testing.T) {
	m, _ := New(10)
	mustApply(t, m, Edit{Adds: []File{{Level: 1, Num: 5, Smallest: b("a"), Largest: b("m")}}})

	err := m.Apply(Edit{Adds: []File{{Level: 9}}, Dels: []Del{{Level: 0, Num: 7}}})
	if !errors.Is(err, ErrParam) {
		t.Fatalf("want ErrParam, got %v", err)
	}
	err = m.Apply(Edit{
		Dels: []Del{{Level: 0, Num: 7}},
		Adds: []File{{Level: 0, Num: 5, Smallest: b("x"), Largest: b("y")}},
	})
	if !errors.Is(err, ErrNoFile) {
		t.Fatalf("want ErrNoFile, got %v", err)
	}
	err = m.Apply(Edit{
		Adds:     []File{{Level: 0, Num: 5, Smallest: b("x"), Largest: b("y")}},
		NextFile: pu(1),
	})
	if !errors.Is(err, ErrDupFile) {
		t.Fatalf("want ErrDupFile, got %v", err)
	}
	err = m.Apply(Edit{NextFile: pu(1), LogNumber: pu(100)})
	if !errors.Is(err, ErrRegress) {
		t.Fatalf("want ErrRegress, got %v", err)
	}
	err = m.Apply(Edit{
		Adds:      []File{{Level: 1, Num: 8, Smallest: b("a"), Largest: b("z")}},
		LogNumber: pu(9),
	})
	if !errors.Is(err, ErrLogAhead) {
		t.Fatalf("want ErrLogAhead, got %v", err)
	}
	err = m.Apply(Edit{
		Adds:      []File{{Level: 0, Num: 20, Smallest: b("a"), Largest: b("b")}},
		LogNumber: pu(21),
	})
	if !errors.Is(err, ErrLogAhead) {
		t.Fatalf("equal log/next: want ErrLogAhead, got %v", err)
	}
	mustApply(t, m, Edit{
		Adds:      []File{{Level: 0, Num: 20, Smallest: b("a"), Largest: b("b")}},
		LogNumber: pu(20),
	})
}

func TestNextFileRegressAndSilentRaise(t *testing.T) {
	m, _ := New(10)
	if m.View().NextFile != 2 {
		t.Fatalf("init next = %d", m.View().NextFile)
	}
	if err := m.Apply(Edit{NextFile: pu(1)}); !errors.Is(err, ErrRegress) {
		t.Fatalf("want ErrRegress, got %v", err)
	}
	mustApply(t, m, Edit{
		Adds:     []File{{Level: 0, Num: 9, Smallest: b("a"), Largest: b("b")}},
		NextFile: pu(2),
	})
	if got := m.View().NextFile; got != 10 {
		t.Fatalf("NextFile = %d, want 10", got)
	}
	mustApply(t, m, Edit{NextFile: pu(50)})
	if got := m.View().NextFile; got != 50 {
		t.Fatalf("NextFile = %d, want 50", got)
	}
}

func TestRotationThreshold(t *testing.T) {
	m, _ := New(3)
	mustApply(t, m, Edit{Adds: []File{{Level: 0, Num: 5, Smallest: b("a"), Largest: b("c")}}})
	mustApply(t, m, Edit{LogNumber: pu(1)})
	d := m.Disk()
	if d.Current != 1 || len(d.Manifests[1]) != 3 {
		t.Fatalf("at threshold: current=%d len=%d", d.Current, len(d.Manifests[1]))
	}
	if got := m.View().NextFile; got != 6 {
		t.Fatalf("NextFile after add 5 = %d, want 6", got)
	}
	mustApply(t, m, Edit{LastSeq: pu(1)})
	d = m.Disk()
	if d.Current != 6 || len(d.Manifests[6]) != 1 {
		t.Fatalf("after rotate: current=%d recs=%v", d.Current, d.Manifests[6])
	}
	snap := d.Manifests[6][0]
	if !snap.IsSnapshot || snap.Snapshot.NextFile != 7 ||
		snap.Snapshot.LogNumber != 1 || snap.Snapshot.LastSeq != 1 {
		t.Fatalf("snapshot content wrong: %+v", snap)
	}
	if len(snap.Snapshot.Files[0]) != 1 || snap.Snapshot.Files[0][0].Num != 5 {
		t.Fatalf("snapshot files wrong: %+v", snap.Snapshot.Files)
	}
	rv, err := Recover(d)
	if err != nil {
		t.Fatal(err)
	}
	if !versionsEqual(rv, m.View()) {
		t.Fatalf("recover %+v != view %+v", rv, m.View())
	}
}

func TestExplicitRotate(t *testing.T) {
	m, _ := New(100)
	num, err := m.Rotate()
	if err != nil || num != 2 {
		t.Fatalf("Rotate = %d, %v", num, err)
	}
	d := m.Disk()
	if d.Current != 2 || d.Manifests[2][0].Snapshot.NextFile != 3 {
		t.Fatalf("rotate disk wrong: %+v", d)
	}
	if m.View().NextFile != 3 {
		t.Fatalf("active NextFile = %d, want 3", m.View().NextFile)
	}
}

func TestRotateUnflippedRecovery(t *testing.T) {
	m, _ := New(100)
	mustApply(t, m, Edit{Adds: []File{{Level: 0, Num: 7, Smallest: b("a"), Largest: b("b")}}})
	num, err := m.RotateUnflipped()
	if err != nil || num != 8 {
		t.Fatalf("RotateUnflipped = %d, %v", num, err)
	}
	d := m.Disk()
	if d.Current != 1 {
		t.Fatalf("CURRENT flipped unexpectedly: %d", d.Current)
	}
	if _, ok := d.Manifests[8]; !ok {
		t.Fatalf("orphan manifest 8 missing")
	}
	if m.View().NextFile != 9 {
		t.Fatalf("active NextFile = %d, want 9", m.View().NextFile)
	}
	before := len(d.Manifests[1])
	mustApply(t, m, Edit{LogNumber: pu(2)})
	if len(m.Disk().Manifests[1]) != before+1 {
		t.Fatalf("edit did not append to old manifest")
	}
	rv, err := Recover(m.Disk())
	if err != nil {
		t.Fatal(err)
	}
	if !versionsEqual(rv, m.View()) {
		t.Fatalf("recover %+v != view %+v", rv, m.View())
	}
	if rv.NextFile != 9 {
		t.Fatalf("recovered NextFile = %d, want 9", rv.NextFile)
	}
}

func TestOpenRotatesAndKeepsOld(t *testing.T) {
	m, _ := New(3)
	mustApply(t, m, Edit{Adds: []File{{Level: 0, Num: 5, Smallest: b("a"), Largest: b("c")}}})
	d := m.Disk()
	oldManifests := len(d.Manifests)
	om, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	nd := om.Disk()
	if len(nd.Manifests) != oldManifests+1 {
		t.Fatalf("Open should add exactly one manifest, got %d -> %d", oldManifests, len(nd.Manifests))
	}
	if _, ok := nd.Manifests[d.Current]; !ok {
		t.Fatalf("old CURRENT manifest %d removed", d.Current)
	}
	if len(nd.Manifests[nd.Current]) != 1 || !nd.Manifests[nd.Current][0].IsSnapshot {
		t.Fatalf("new manifest must be a single snapshot")
	}
}

func TestRecoverErrors(t *testing.T) {
	base := func() Disk {
		m, _ := New(3)
		return m.Disk()
	}

	d := base()
	d.Current = 99
	if _, err := Recover(d); !errors.Is(err, ErrNoCurrent) {
		t.Fatalf("missing current: %v", err)
	}

	d = base()
	recs := d.Manifests[1]
	recs[0].Torn = true
	d.Manifests[1] = recs
	_, err := Recover(d)
	checkCorrupt(t, err, 0, nil)

	d = base()
	good := Edit{LogNumber: pu(1)}
	bad := Edit{Adds: []File{{Level: 1, Num: 2, Smallest: b("a"), Largest: b("m")}}}
	overlap := Edit{Adds: []File{{Level: 1, Num: 3, Smallest: b("m"), Largest: b("z")}}}
	d.Manifests[1] = append(d.Manifests[1],
		Record{Edit: &bad},
		Record{Edit: &overlap},
	)
	_, err = Recover(d)
	checkCorrupt(t, err, 2, ErrOverlap)

	d = base()
	d.Manifests[1] = append(d.Manifests[1],
		Record{Edit: &good},
		Record{IsSnapshot: true, Snapshot: d.Manifests[1][0].Snapshot},
	)
	_, err = Recover(d)
	checkCorrupt(t, err, 2, ErrCorrupt)

	// Torn edit in the middle is corrupt.
	d = base()
	d.Manifests[1] = append(d.Manifests[1],
		Record{Edit: &good, Torn: true},
		Record{Edit: &good},
	)
	_, err = Recover(d)
	checkCorrupt(t, err, 1, ErrCorrupt)

	// Torn edit as the last record is ignored; version equals the good prefix.
	d = base()
	m2, _ := New(3)
	m2.Apply(good)
	d.Manifests[1] = append(d.Manifests[1],
		Record{Edit: &good},
		Record{Edit: &Edit{LogNumber: pu(42)}, Torn: true},
	)
	rv, err := Recover(d)
	if err != nil {
		t.Fatal(err)
	}
	if rv.LogNumber != 1 {
		t.Fatalf("torn tail not ignored: LogNumber=%d", rv.LogNumber)
	}
	if !versionsEqual(rv, m2.View()) {
		t.Fatalf("torn-tail recover %+v != prefix view %+v", rv, m2.View())
	}

	// Recovery replays without steps four/five: a LogNumber ahead of the
	// replay NextFile is accepted (it was valid at write time).
	d = base()
	d.Manifests[1] = append(d.Manifests[1],
		Record{Edit: &Edit{Adds: []File{{Level: 0, Num: 5, Smallest: b("a"), Largest: b("b")}}, LogNumber: pu(5)}},
	)
	rv, err = Recover(d)
	if err != nil {
		t.Fatalf("replay should skip ErrLogAhead: %v", err)
	}
	if rv.LogNumber != 5 {
		t.Fatalf("replayed LogNumber = %d, want 5", rv.LogNumber)
	}

	// ErrNoFile during replay maps to ErrCorrupt with record index.
	d = base()
	d.Manifests[1] = append(d.Manifests[1],
		Record{Edit: &Edit{Dels: []Del{{Level: 0, Num: 10}}}},
	)
	_, err = Recover(d)
	checkCorrupt(t, err, 1, ErrNoFile)
	_ = fmt.Sprintf
}
