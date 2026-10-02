package fslog

import (
	"errors"
	"testing"
)

const modP uint64 = 1000003

func exampleFuncs() Funcs {
	return Funcs{
		Evolve: func(k uint64) uint64 { return (3*k + 11) % modP },
		Rekey:  func(k, i uint64) uint64 { return (5*k + i + 7) % modP },
		Mac: func(k, i, typ, ts uint64, data []byte) uint64 {
			s := k + 31*i + 101*typ + 7*ts
			for _, b := range data {
				s += uint64(b)
			}
			return s % modP
		},
	}
}

func mustNew(t *testing.T, f Funcs, k0, capacity uint64, maxData int) *Log {
	t.Helper()
	l, err := New(f, k0, capacity, maxData)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return l
}

func mustOp(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("op: %v", err)
	}
}

func opKind(err error) OpKind {
	var oe *OpError
	if errors.As(err, &oe) {
		return oe.Kind
	}
	return -1
}

func checkReport(t *testing.T, got Report, want Report) {
	t.Helper()
	if got != want {
		t.Fatalf("report mismatch:\n got %+v\nwant %+v", got, want)
	}
}

// Spec example: Append(10,"A"), Append(10,"B"), Seal(12) with k0=100.
func TestSpecExampleTagsAndKeys(t *testing.T) {
	f := exampleFuncs()
	l := mustNew(t, f, 100, 10, 16)
	mustOp(t, l.Append(10, []byte("A")))
	mustOp(t, l.Append(10, []byte("B")))
	mustOp(t, l.Seal(12))

	es := l.Entries()
	if len(es) != 3 {
		t.Fatalf("want 3 entries, got %d", len(es))
	}
	wantTags := []uint64{235, 478, 1241}
	wantTyps := []uint64{TypData, TypData, TypSeal}
	for i, e := range es {
		if e.Index != uint64(i) || e.Typ != wantTyps[i] || e.Tag != wantTags[i] {
			t.Fatalf("entry %d = %+v, want index %d typ %d tag %d", i, e, i, wantTyps[i], wantTags[i])
		}
	}
	if string(es[2].Data) != "2" {
		t.Fatalf("seal data = %q, want %q", es[2].Data, "2")
	}

	checkReport(t, Verify(es, []Checkpoint{{Seq: 0, Key: 100}}, f), Report{
		Verdict: VerdictOK, Verified: 3, EvolveCount: 3, Sealed: true,
	})
	checkReport(t, Verify(es, []Checkpoint{{Seq: 1, Key: 311}}, f), Report{
		Verdict: VerdictOK, Verified: 2, Unverifiable: 1, EvolveCount: 2, Sealed: true,
	})
	checkReport(t, l.SelfVerify([]Checkpoint{{Seq: 0, Key: 100}}), Report{
		Verdict: VerdictOK, Verified: 3, EvolveCount: 3, Sealed: true,
	})
}

// Spec example: tamper at position 1, and time-regression beats tag check.
func TestSpecExampleTamperAndOrder(t *testing.T) {
	f := exampleFuncs()
	l := mustNew(t, f, 100, 10, 16)
	mustOp(t, l.Append(10, []byte("A")))
	mustOp(t, l.Append(10, []byte("B")))
	mustOp(t, l.Seal(12))
	es := l.Entries()

	tampered := cloneEntries(es)
	tampered[1].Data = []byte("C")
	if got := f.Mac(311, 1, 0, 10, []byte("C")); got != 479 {
		t.Fatalf("sanity mac(C) = %d, want 479", got)
	}
	checkReport(t, Verify(tampered, []Checkpoint{{Seq: 0, Key: 100}}, f), Report{
		Verdict: VerdictTamper, Pos: 1, Verified: 1, EvolveCount: 1,
	})

	tampered[1].Ts = 5
	checkReport(t, Verify(tampered, []Checkpoint{{Seq: 0, Key: 100}}, f), Report{
		Verdict: VerdictTimeRegression, Pos: 1, Verified: 1, EvolveCount: 1,
	})
}

// Spec example: checkpoint conflict, truncation, replay, gap.
func TestSpecExampleConflictTruncationReplayGap(t *testing.T) {
	f := exampleFuncs()
	l := mustNew(t, f, 100, 10, 16)
	mustOp(t, l.Append(10, []byte("A")))
	mustOp(t, l.Append(10, []byte("B")))
	mustOp(t, l.Seal(12))
	es := l.Entries()

	checkReport(t, Verify(es, []Checkpoint{{Seq: 0, Key: 100}, {Seq: 2, Key: 945}}, f), Report{
		Verdict: VerdictCheckpointConflict, Pos: 2, Verified: 2, EvolveCount: 2, Sealed: false,
	})

	checkReport(t, Verify(es[:1], []Checkpoint{{Seq: 0, Key: 100}, {Seq: 2, Key: 944}}, f), Report{
		Verdict: VerdictTruncation, Pos: 1, Verified: 1, EvolveCount: 1,
	})

	replay := []Entry{es[0], es[1], es[1]}
	checkReport(t, Verify(replay, []Checkpoint{{Seq: 0, Key: 100}}, f), Report{
		Verdict: VerdictReplay, Pos: 2, Verified: 2, EvolveCount: 2,
	})

	gap := []Entry{es[0], es[2]}
	checkReport(t, Verify(gap, []Checkpoint{{Seq: 0, Key: 100}}, f), Report{
		Verdict: VerdictGap, Pos: 1, Verified: 1, EvolveCount: 1,
	})
}

// Spec rekey example: Append(10,"A"), Rekey(10), Seal(12).
func TestSpecRekeyExample(t *testing.T) {
	f := exampleFuncs()
	l := mustNew(t, f, 100, 10, 16)
	mustOp(t, l.Append(10, []byte("A")))
	mustOp(t, l.Rekey(10))
	mustOp(t, l.Seal(12))

	es := l.Entries()
	wantTags := []uint64{235, 663, 1860}
	for i, e := range es {
		if e.Tag != wantTags[i] {
			t.Fatalf("entry %d tag = %d, want %d", i, e.Tag, wantTags[i])
		}
	}
	if string(es[1].Data) != "1" {
		t.Fatalf("rekey data = %q, want %q", es[1].Data, "1")
	}

	checkReport(t, Verify(es, []Checkpoint{{Seq: 0, Key: 100}}, f), Report{
		Verdict: VerdictOK, Verified: 3, EvolveCount: 2, RekeyCount: 1, Sealed: true,
	})
	checkReport(t, Verify(es, []Checkpoint{{Seq: 0, Key: 100}, {Seq: 2, Key: 1563}}, f), Report{
		Verdict: VerdictOK, Verified: 3, EvolveCount: 2, RekeyCount: 1, Sealed: true,
	})
	checkReport(t, Verify(es, []Checkpoint{{Seq: 0, Key: 100}, {Seq: 2, Key: 944}}, f), Report{
		Verdict: VerdictCheckpointConflict, Pos: 2, Verified: 2, EvolveCount: 1, RekeyCount: 1,
	})
	checkReport(t, Verify(es, []Checkpoint{{Seq: 2, Key: 1563}}, f), Report{
		Verdict: VerdictOK, Pos: 0, Verified: 1, Unverifiable: 2, EvolveCount: 1, RekeyCount: 0, Sealed: true,
	})
}
