package auditlog

import "testing"

const linearMod uint64 = 1000003

// Deterministic functions from the worked example in the specification.
func linearEvolve(k uint64) uint64 { return (3*k + 11) % linearMod }

func linearRekey(k uint64, i int64) uint64 {
	return (5*k + uint64(i) + 7) % linearMod
}

func linearMac(k uint64, i int64, typ int32, ts int64, data []byte) uint64 {
	var sum uint64
	for _, b := range data {
		sum += uint64(b)
	}
	return (k + 31*uint64(i) + 101*uint64(typ) + 7*uint64(ts) + sum) % linearMod
}

func newLinearLog(t *testing.T, k0 uint64, cap, maxData int) *Log {
	t.Helper()
	l, err := New(k0, cap, maxData, linearEvolve, linearRekey, linearMac)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestWorkedExampleTagsAndKeys(t *testing.T) {
	l := newLinearLog(t, 100, 100, 64)
	if err := l.Append(10, []byte("A")); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(10, []byte("B")); err != nil {
		t.Fatal(err)
	}
	if err := l.Seal(12); err != nil {
		t.Fatal(err)
	}
	es := l.Entries()
	wantTags := []uint64{235, 478, 1241}
	for i, want := range wantTags {
		if es[i].Tag != want {
			t.Fatalf("entry %d tag = %d, want %d", i, es[i].Tag, want)
		}
	}
	if es[0].Index != 0 || es[1].Index != 1 || es[2].Index != 2 {
		t.Fatalf("indices = %d %d %d", es[0].Index, es[1].Index, es[2])
	}
	if string(es[2].Data) != "2" {
		t.Fatalf("seal data = %q", es[2].Data)
	}

	r, err := l.Verify(es, []Checkpoint{{0, 100}})
	if err != nil || !r.OK() {
		t.Fatalf("verify = %+v, %v", r, err)
	}
	if r.Verified != 3 || r.EvolveCalls != 3 || r.RekeyCalls != 0 || !r.Sealed {
		t.Fatalf("report = %+v", r)
	}

	r, _ = l.Verify(es, []Checkpoint{{1, 311}})
	if !r.OK() || r.Verified != 2 || r.Unverifiable != 1 || r.EvolveCalls != 2 {
		t.Fatalf("late-start report = %+v", r)
	}

	r, _ = l.Verify(es, []Checkpoint{{0, 100}, {2, 945}})
	if r.Kind != KindCheckpointConflict || r.Pos != 2 || r.Verified != 2 {
		t.Fatalf("conflict report = %+v", r)
	}

	r, _ = l.Verify(es[:1], []Checkpoint{{0, 100}, {2, 944}})
	if r.Kind != KindTruncated || r.Pos != 1 {
		t.Fatalf("truncation report = %+v", r)
	}

	r, _ = l.Verify(es, []Checkpoint{{0, 100}, {3, 2843}})
	if !r.OK() {
		t.Fatalf("checkpoint at m matching = %+v", r)
	}
	r, _ = l.Verify(es, []Checkpoint{{0, 100}, {3, 2844}})
	if r.Kind != KindCheckpointConflict || r.Pos != 3 {
		t.Fatalf("checkpoint at m mismatch = %+v", r)
	}

	// j0 == m: nothing verified, nothing unverified, exact match succeeds.
	r, _ = l.Verify(es, []Checkpoint{{3, 2843}})
	if !r.OK() || r.Verified != 0 || r.Unverifiable != 3 {
		t.Fatalf("j0=m report = %+v", r)
	}
}

func TestTamperAndRollbackOrdering(t *testing.T) {
	l := newLinearLog(t, 100, 100, 64)
	_ = l.Append(10, []byte("A"))
	_ = l.Append(10, []byte("B"))
	_ = l.Seal(12)

	es := l.Entries()
	es[1].Data = []byte("C") // correct tag would be 479
	r, _ := l.Verify(es, []Checkpoint{{0, 100}})
	if r.Kind != KindTampered || r.Pos != 1 || r.Verified != 1 || r.EvolveCalls != 1 {
		t.Fatalf("tamper report = %+v", r)
	}

	es = l.Entries()
	es[1].Data = []byte("C")
	es[1].Ts = 5 // rollback is checked before the tag
	r, _ = l.Verify(es, []Checkpoint{{0, 100}})
	if r.Kind != KindTimeRollback || r.Pos != 1 || r.Verified != 1 {
		t.Fatalf("rollback-before-tag report = %+v", r)
	}
}

func TestReplayGapAppendAfterSeal(t *testing.T) {
	l := newLinearLog(t, 100, 100, 64)
	_ = l.Append(10, []byte("A"))
	_ = l.Append(10, []byte("B"))
	_ = l.Seal(12)
	es := l.Entries()

	// [e0, e1, e1]: index 1 < 2 -> replay at 2.
	replay := []*Entry{es[0], es[1], es[1]}
	r, _ := l.Verify(replay, []Checkpoint{{0, 100}})
	if r.Kind != KindReplay || r.Pos != 2 || r.Verified != 2 {
		t.Fatalf("replay report = %+v", r)
	}

	// [e0, seal@2]: index 2 > 1 -> gap at 1 (gap precedes append-after-seal).
	r, _ = l.Verify([]*Entry{es[0], es[2]}, []Checkpoint{{0, 100}})
	if r.Kind != KindGap || r.Pos != 1 || r.Verified != 1 {
		t.Fatalf("gap report = %+v", r)
	}

	// A record with a valid index right after a real seal is append-after-seal.
	// Use a log sealed at index 1: A (key 100 -> 311), seal (key 311 -> 944).
	sealed1 := newLinearLog(t, 100, 100, 64)
	_ = sealed1.Append(10, []byte("A"))
	_ = sealed1.Seal(12)
	s1 := sealed1.Entries()
	tag := linearMac(944, 2, TypData, 12, []byte("X"))
	trailer := &Entry{Index: 2, Typ: TypData, Ts: 12, Data: []byte("X"), Tag: tag}
	r, _ = l.Verify([]*Entry{s1[0], s1[1], trailer}, []Checkpoint{{0, 100}})
	if r.Kind != KindAppendAfterSeal || r.Pos != 2 || r.Verified != 2 {
		t.Fatalf("append-after-seal report = %+v", r)
	}
}

func TestRekeyExampleAndCounts(t *testing.T) {
	l := newLinearLog(t, 100, 100, 64)
	if err := l.Append(10, []byte("A")); err != nil {
		t.Fatal(err)
	}
	if err := l.Rekey(10); err != nil {
		t.Fatal(err)
	}
	if err := l.Seal(12); err != nil {
		t.Fatal(err)
	}
	es := l.Entries()
	wantTags := []uint64{235, 663, 1860}
	for i, want := range wantTags {
		if es[i].Tag != want {
			t.Fatalf("entry %d tag = %d want %d", i, es[i].Tag, want)
		}
	}

	r, _ := l.Verify(es, []Checkpoint{{0, 100}})
	if !r.OK() || r.Verified != 3 || r.EvolveCalls != 2 || r.RekeyCalls != 1 || !r.Sealed {
		t.Fatalf("report = %+v", r)
	}
	r, _ = l.Verify(es, []Checkpoint{{0, 100}, {2, 1563}})
	if !r.OK() {
		t.Fatalf("matching rekey checkpoint = %+v", r)
	}
	r, _ = l.Verify(es, []Checkpoint{{0, 100}, {2, 944}})
	if r.Kind != KindCheckpointConflict || r.Pos != 2 || r.Verified != 2 {
		t.Fatalf("evolve-mistake report = %+v", r)
	}
	r, _ = l.Verify(es, []Checkpoint{{2, 1563}})
	if !r.OK() || r.Unverifiable != 2 || r.Verified != 1 || r.EvolveCalls != 1 || r.RekeyCalls != 0 {
		t.Fatalf("late cp report = %+v", r)
	}
}

func TestNoRollbackCheckAtJ0(t *testing.T) {
	l := newLinearLog(t, 100, 100, 64)
	_ = l.Append(10, []byte("A"))
	_ = l.Append(10, []byte("B"))
	_ = l.Seal(12)
	es := l.Entries()
	// Start at position 1 with e1.Ts below e0.Ts: e0 is never inspected and
	// no previous-entry comparison exists at j0, so verification passes.
	es[1].Ts = 3
	es[1].Tag = linearMac(311, 1, TypData, 3, []byte("B"))
	r, _ := l.Verify(es, []Checkpoint{{1, 311}})
	if !r.OK() {
		t.Fatalf("report = %+v", r)
	}

	// j0 beyond the end: every entry unverifiable and the tail is truncated.
	full := l.Entries()
	r, _ = l.Verify(full, []Checkpoint{{9, 1}})
	if r.Kind != KindTruncated || r.Pos != int64(len(full)) ||
		r.Unverifiable != int64(len(full)) || r.Verified != 0 {
		t.Fatalf("j0>m report = %+v", r)
	}
}

func TestContentMismatch(t *testing.T) {
	l := newLinearLog(t, 100, 100, 64)
	_ = l.Append(10, []byte("A"))
	_ = l.Rekey(10)
	_ = l.Seal(12)
	es := l.Entries()

	// Rekey payload wrong but tag forged consistently -> content mismatch.
	es[1].Data = []byte("9")
	es[1].Tag = linearMac(311, 1, TypRekey, 10, []byte("9"))
	r, _ := l.Verify(es, []Checkpoint{{0, 100}})
	if r.Kind != KindContentMismatch || r.Pos != 1 || r.Verified != 1 {
		t.Fatalf("rekey content mismatch = %+v", r)
	}

	es = l.Entries()
	es[2].Data = []byte("9")
	es[2].Tag = linearMac(1563, 2, TypSeal, 12, []byte("9"))
	r, _ = l.Verify(es, []Checkpoint{{0, 100}})
	if r.Kind != KindContentMismatch || r.Pos != 2 || r.Verified != 2 {
		t.Fatalf("seal content mismatch = %+v", r)
	}
}

func TestIllegalTypIsTampered(t *testing.T) {
	l := newLinearLog(t, 100, 100, 64)
	_ = l.Append(10, []byte("A"))
	es := l.Entries()
	es[0].Typ = 7
	r, _ := l.Verify(es, []Checkpoint{{0, 100}})
	if r.Kind != KindTampered || r.Pos != 0 {
		t.Fatalf("illegal typ report = %+v", r)
	}
}

func TestRejectionsDoNotMutate(t *testing.T) {
	l := newLinearLog(t, 100, 5, 4)

	if err := l.Append(-1, []byte("A")); err != ErrInvalidArg {
		t.Fatalf("ts<0: %v", err)
	}
	if err := l.Append(10, []byte("ABCDE")); err != ErrInvalidArg {
		t.Fatalf("oversize: %v", err)
	}
	if err := l.Rekey(-1); err != ErrInvalidArg {
		t.Fatalf("rekey ts<0: %v", err)
	}
	if err := l.Seal(-1); err != ErrInvalidArg {
		t.Fatalf("seal ts<0: %v", err)
	}

	if err := l.Append(10, []byte("A")); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(10, []byte("B")); err != nil {
		t.Fatalf("equal ts: %v", err)
	}
	if err := l.Append(9, []byte("C")); err != ErrTimeRollback {
		t.Fatalf("rollback: %v", err)
	}

	// cap=5: non-seal slots are indices 0..3, seal uses index 4.
	if err := l.Rekey(11); err != nil { // index 2
		t.Fatalf("rekey: %v", err)
	}
	if err := l.Append(12, []byte("D")); err != nil { // index 3
		t.Fatalf("append d: %v", err)
	}
	if err := l.Append(12, []byte("E")); err != ErrCapFull {
		t.Fatalf("cap full: %v", err)
	}
	if err := l.Rekey(12); err != ErrCapFull {
		t.Fatalf("rekey cap full: %v", err)
	}
	if err := l.Append(11, []byte("E")); err != ErrTimeRollback {
		t.Fatalf("rollback precedence: %v", err)
	}

	if i, _, err := l.Export(); err != nil || i != 4 {
		t.Fatalf("export = i %d, %v", i, err)
	}
	if err := l.Seal(12); err != nil {
		t.Fatalf("seal: %v", err)
	}
	for _, call := range []func() error{
		func() error { return l.Append(13, []byte("Z")) },
		func() error { return l.Rekey(13) },
		func() error { return l.Seal(13) },
	} {
		if err := call(); err != ErrSealed {
			t.Fatalf("post-seal write: %v", err)
		}
	}
	if _, _, err := l.Export(); err != ErrSealed {
		t.Fatalf("post-seal export: %v", err)
	}

	es := l.Entries()
	if len(es) != 5 || es[2].Typ != TypRekey || es[4].Typ != TypSeal {
		t.Fatalf("unexpected entries: %v", es)
	}
	r1, _ := l.SelfVerify([]Checkpoint{{0, 100}})
	r2, _ := l.Verify(es, []Checkpoint{{0, 100}})
	if r1 != r2 || !r1.OK() || !r1.Sealed {
		t.Fatalf("self=%+v verify=%+v", r1, r2)
	}

	es[0].Tag = ^uint64(0)
	es[0].Data[0] = 'Z'
	got := l.Entries()
	if got[0].Tag == ^uint64(0) || got[0].Data[0] != 'A' {
		t.Fatal("Entries() did not return an independent copy")
	}
}

func TestTimestampBounds(t *testing.T) {
	l := newLinearLog(t, 100, 10, 0)
	if err := l.Append(0, nil); err != nil {
		t.Fatalf("ts=0: %v", err)
	}
	if err := l.Append(1_000_000_000_000_000, nil); err != nil {
		t.Fatalf("ts=1e15: %v", err)
	}
	if err := l.Append(1_000_000_000_000_001, nil); err != ErrInvalidArg {
		t.Fatalf("ts>1e15: %v", err)
	}
	if err := l.Seal(1_000_000_000_000_000); err != nil {
		t.Fatalf("seal at max ts: %v", err)
	}
	r, _ := l.SelfVerify([]Checkpoint{{0, 100}})
	if !r.OK() || !r.Sealed {
		t.Fatalf("report = %+v", r)
	}
}

func TestConfigAndCheckpointValidation(t *testing.T) {
	if _, err := New(1, 1, 0, linearEvolve, linearRekey, linearMac); err != ErrInvalidConfig {
		t.Fatalf("cap 1: %v", err)
	}
	if _, err := New(1, 1_000_001, 0, linearEvolve, linearRekey, linearMac); err != ErrInvalidConfig {
		t.Fatalf("cap too big: %v", err)
	}
	if _, err := New(1, 10, -1, linearEvolve, linearRekey, linearMac); err != ErrInvalidConfig {
		t.Fatalf("maxData -1: %v", err)
	}
	if _, err := New(1, 10, 4097, linearEvolve, linearRekey, linearMac); err != ErrInvalidConfig {
		t.Fatalf("maxData too big: %v", err)
	}
	if _, err := New(1, 10, 0, nil, linearRekey, linearMac); err != ErrInvalidConfig {
		t.Fatalf("nil evolve: %v", err)
	}
	l := newLinearLog(t, 1, 10, 0)
	if _, err := l.Verify(nil, nil); err != ErrInvalidArg {
		t.Fatalf("empty cps: %v", err)
	}
	if _, err := l.Verify(nil, []Checkpoint{{0, 1}, {0, 2}}); err != ErrInvalidArg {
		t.Fatalf("duplicate cps: %v", err)
	}
	if _, err := l.Verify(nil, []Checkpoint{{-1, 1}}); err != ErrInvalidArg {
		t.Fatalf("negative cp: %v", err)
	}
	if _, err := l.SelfVerify(nil); err != ErrInvalidArg {
		t.Fatalf("selfverify empty cps: %v", err)
	}
}
