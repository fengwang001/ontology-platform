package fslog

import (
	"crypto/sha256"
	"encoding/binary"
	"testing"
)

// keyChain returns the key schedule for entries starting from k0:
// keys[t] is the key before processing entry t, keys[len(entries)] the final key.
func keyChain(f Funcs, entries []Entry, k0 uint64) []uint64 {
	keys := make([]uint64, len(entries)+1)
	keys[0] = k0
	for t, e := range entries {
		if e.Typ == TypRekey {
			k0 = f.Rekey(k0, uint64(t))
		} else {
			k0 = f.Evolve(k0)
		}
		keys[t+1] = k0
	}
	return keys
}

func TestSealBlocksWritesAndExport(t *testing.T) {
	f := exampleFuncs()
	l := mustNew(t, f, 100, 10, 16)
	mustOp(t, l.Append(10, []byte("A")))
	i, k, err := l.Export()
	if err != nil || i != 1 || k != 311 {
		t.Fatalf("export before seal = (%d,%d,%v), want (1,311,nil)", i, k, err)
	}
	mustOp(t, l.Seal(12))
	if got := opKind(l.Append(13, nil)); got != OpSealed {
		t.Fatalf("append after seal kind = %v, want OpSealed", got)
	}
	if got := opKind(l.Rekey(13)); got != OpSealed {
		t.Fatalf("rekey after seal kind = %v, want OpSealed", got)
	}
	if got := opKind(l.Seal(13)); got != OpSealed {
		t.Fatalf("re-seal kind = %v, want OpSealed", got)
	}
	if _, _, err := l.Export(); opKind(err) != OpSealed {
		t.Fatalf("export after seal err = %v, want OpSealed", err)
	}
	if n := len(l.Entries()); n != 2 {
		t.Fatalf("entries after rejected ops = %d, want 2", n)
	}
}

func TestCapacityReservesSealSlot(t *testing.T) {
	f := exampleFuncs()
	l := mustNew(t, f, 100, 2, 16)
	mustOp(t, l.Append(1, nil))
	if got := opKind(l.Append(2, nil)); got != OpCapacityFull {
		t.Fatalf("append at cap-1 kind = %v, want OpCapacityFull", got)
	}
	if got := opKind(l.Rekey(2)); got != OpCapacityFull {
		t.Fatalf("rekey at cap-1 kind = %v, want OpCapacityFull", got)
	}
	mustOp(t, l.Seal(2))
	if n := len(l.Entries()); n != 2 {
		t.Fatalf("entries = %d, want 2 (cap)", n)
	}
}

func TestRekeyConsumesCapacityAndDoesNotSeal(t *testing.T) {
	f := exampleFuncs()
	l := mustNew(t, f, 100, 3, 16)
	mustOp(t, l.Rekey(1))
	mustOp(t, l.Append(2, []byte("x"))) // not sealed: append still accepted
	if got := opKind(l.Append(3, nil)); got != OpCapacityFull {
		t.Fatalf("append at cap-1 kind = %v, want OpCapacityFull", got)
	}
	mustOp(t, l.Seal(3))
	rep := l.SelfVerify([]Checkpoint{{Seq: 0, Key: 100}})
	if rep.Verdict != VerdictOK || rep.RekeyCount != 1 || rep.EvolveCount != 2 || !rep.Sealed {
		t.Fatalf("report = %+v", rep)
	}
}

func TestTimestampEqualAllowedMinusOneRejected(t *testing.T) {
	f := exampleFuncs()
	l := mustNew(t, f, 100, 10, 16)
	mustOp(t, l.Append(10, nil))
	mustOp(t, l.Append(10, nil)) // equal timestamps are allowed
	if got := opKind(l.Append(9, nil)); got != OpTimeRegression {
		t.Fatalf("ts-1 kind = %v, want OpTimeRegression", got)
	}
	if got := opKind(l.Append(MaxTs+1, nil)); got != OpInvalidArg {
		t.Fatalf("ts overflow kind = %v, want OpInvalidArg", got)
	}
	mustOp(t, l.Append(MaxTs, nil)) // boundary accepted
	if n := len(l.Entries()); n != 3 {
		t.Fatalf("entries = %d, want 3 (rejected ops changed nothing)", n)
	}
}

func TestInvalidConfigRejected(t *testing.T) {
	f := exampleFuncs()
	bad := []struct {
		cap     uint64
		maxData int
	}{
		{1, 16}, {1000001, 16}, {10, -1}, {10, 4097},
	}
	for _, c := range bad {
		if _, err := New(f, 100, c.cap, c.maxData); opKind(err) != OpInvalidArg {
			t.Fatalf("New(cap=%d,maxData=%d) err = %v, want OpInvalidArg", c.cap, c.maxData, err)
		}
	}
	if _, err := New(Funcs{}, 100, 10, 16); opKind(err) != OpInvalidArg {
		t.Fatalf("New(nil funcs) err = %v, want OpInvalidArg", err)
	}
	for _, c := range []struct {
		cap     uint64
		maxData int
	}{{2, 0}, {1000000, 4096}} {
		if _, err := New(f, 100, c.cap, c.maxData); err != nil {
			t.Fatalf("New(cap=%d,maxData=%d) err = %v, want nil", c.cap, c.maxData, err)
		}
	}
	l := mustNew(t, f, 100, 10, 0)
	mustOp(t, l.Append(1, nil))
	if got := opKind(l.Append(2, []byte("x"))); got != OpInvalidArg {
		t.Fatalf("data over maxData kind = %v, want OpInvalidArg", got)
	}
}

func TestInvalidCheckpointSetRejected(t *testing.T) {
	f := exampleFuncs()
	l := mustNew(t, f, 100, 10, 16)
	mustOp(t, l.Append(1, nil))
	es := l.Entries()
	for name, cps := range map[string][]Checkpoint{
		"empty":     {},
		"dup-seq":   {{Seq: 0, Key: 100}, {Seq: 0, Key: 100}},
		"neg-seq":   {{Seq: -1, Key: 100}},
		"nil-funcs": {{Seq: 0, Key: 100}},
	} {
		fn := f
		if name == "nil-funcs" {
			fn = Funcs{}
		}
		if got := Verify(es, cps, fn).Verdict; got != VerdictInvalidArg {
			t.Fatalf("%s: verdict = %v, want invalid-arg", name, got)
		}
	}
}

func TestUnverifiablePrefixNeverRead(t *testing.T) {
	f := exampleFuncs()
	l := mustNew(t, f, 100, 10, 16)
	mustOp(t, l.Append(10, []byte("A")))
	mustOp(t, l.Append(10, []byte("B")))
	mustOp(t, l.Seal(12))
	es := l.Entries()
	// Corrupt entry 0 beyond recognition; the verifier must not look at it.
	es[0] = Entry{Index: 99, Typ: 7, Ts: 5, Data: []byte("junk"), Tag: 1}
	checkReport(t, Verify(es, []Checkpoint{{Seq: 1, Key: 311}}, f), Report{
		Verdict: VerdictOK, Verified: 2, Unverifiable: 1, EvolveCount: 2, Sealed: true,
	})
}

func TestNoTimeRegressionCheckAtJ0(t *testing.T) {
	f := exampleFuncs()
	// Hand-crafted: entry 1 has a smaller timestamp than entry 0 but a valid tag.
	// The writer would reject this, but a verifier starting at j0=1 must not
	// perform the time-regression check on the first checked position.
	es := []Entry{
		{Index: 0, Typ: TypData, Ts: 10, Data: []byte("A"), Tag: f.Mac(100, 0, TypData, 10, []byte("A"))},
		{Index: 1, Typ: TypData, Ts: 5, Data: []byte("B"), Tag: f.Mac(311, 1, TypData, 5, []byte("B"))},
	}
	checkReport(t, Verify(es, []Checkpoint{{Seq: 1, Key: 311}}, f), Report{
		Verdict: VerdictOK, Verified: 1, Unverifiable: 1, EvolveCount: 1,
	})
	checkReport(t, Verify(es, []Checkpoint{{Seq: 0, Key: 100}}, f), Report{
		Verdict: VerdictTimeRegression, Pos: 1, Verified: 1, EvolveCount: 1,
	})
}

func TestGapBeatsAppendAfterSeal(t *testing.T) {
	f := exampleFuncs()
	l := mustNew(t, f, 100, 10, 16)
	mustOp(t, l.Seal(5)) // entry 0 is a seal record
	es := l.Entries()
	// Position 1 holds an entry with Index 2: gap must win over append-after-seal.
	es = append(es, Entry{Index: 2, Typ: TypData, Ts: 6, Data: nil, Tag: 0})
	checkReport(t, Verify(es, []Checkpoint{{Seq: 0, Key: 100}}, f), Report{
		Verdict: VerdictGap, Pos: 1, Verified: 1, EvolveCount: 1,
	})
}

func TestAppendAfterSeal(t *testing.T) {
	f := exampleFuncs()
	l := mustNew(t, f, 100, 10, 16)
	mustOp(t, l.Append(10, []byte("A")))
	mustOp(t, l.Seal(12))
	es := l.Entries()
	// A perfectly valid continuation entry after the seal (key 2843 = evolve(944)).
	es = append(es, Entry{
		Index: 2, Typ: TypData, Ts: 13, Data: []byte("C"),
		Tag: f.Mac(2843, 2, TypData, 13, []byte("C")),
	})
	checkReport(t, Verify(es, []Checkpoint{{Seq: 0, Key: 100}}, f), Report{
		Verdict: VerdictAppendAfterSeal, Pos: 2, Verified: 2, EvolveCount: 2,
	})
}

func TestContentMismatchSealAndRekey(t *testing.T) {
	f := exampleFuncs()
	// Seal record whose Data is not the decimal text of its index; tag is valid for the data.
	sealBad := []Entry{{
		Index: 0, Typ: TypSeal, Ts: 5, Data: []byte("zero"),
		Tag: f.Mac(100, 0, TypSeal, 5, []byte("zero")),
	}}
	checkReport(t, Verify(sealBad, []Checkpoint{{Seq: 0, Key: 100}}, f), Report{
		Verdict: VerdictContentMismatch, Pos: 0,
	})
	// Rekey record with wrong decimal payload.
	rekeyBad := []Entry{{
		Index: 0, Typ: TypRekey, Ts: 5, Data: []byte("1"),
		Tag: f.Mac(100, 0, TypRekey, 5, []byte("1")),
	}}
	checkReport(t, Verify(rekeyBad, []Checkpoint{{Seq: 0, Key: 100}}, f), Report{
		Verdict: VerdictContentMismatch, Pos: 0,
	})
	// Sanity: correct payloads pass.
	sealOK := []Entry{{
		Index: 0, Typ: TypSeal, Ts: 5, Data: []byte("0"),
		Tag: f.Mac(100, 0, TypSeal, 5, []byte("0")),
	}}
	checkReport(t, Verify(sealOK, []Checkpoint{{Seq: 0, Key: 100}}, f), Report{
		Verdict: VerdictOK, Verified: 1, EvolveCount: 1, Sealed: true,
	})
	rekeyOK := []Entry{{
		Index: 0, Typ: TypRekey, Ts: 5, Data: []byte("0"),
		Tag: f.Mac(100, 0, TypRekey, 5, []byte("0")),
	}}
	checkReport(t, Verify(rekeyOK, []Checkpoint{{Seq: 0, Key: 100}}, f), Report{
		Verdict: VerdictOK, Verified: 1, RekeyCount: 1,
	})
}

func TestInvalidTypIsTamperAndMacNotCalled(t *testing.T) {
	f := exampleFuncs()
	f.Mac = func(k, i, typ, ts uint64, data []byte) uint64 {
		if typ > TypRekey {
			t.Fatalf("mac called for invalid typ %d", typ)
		}
		return exampleFuncs().Mac(k, i, typ, ts, data)
	}
	es := []Entry{{Index: 0, Typ: 9, Ts: 5, Data: nil, Tag: 12345}}
	checkReport(t, Verify(es, []Checkpoint{{Seq: 0, Key: 100}}, f), Report{
		Verdict: VerdictTamper, Pos: 0,
	})
}

func TestCheckpointExactlyAtM(t *testing.T) {
	f := exampleFuncs()
	l := mustNew(t, f, 100, 10, 16)
	mustOp(t, l.Append(10, []byte("A")))
	mustOp(t, l.Append(10, []byte("B")))
	mustOp(t, l.Seal(12))
	es := l.Entries()
	keys := keyChain(f, es, 100) // keys[3] = 2843
	if keys[3] != 2843 {
		t.Fatalf("final key = %d, want 2843", keys[3])
	}
	checkReport(t, Verify(es, []Checkpoint{{Seq: 0, Key: 100}, {Seq: 3, Key: 2843}}, f), Report{
		Verdict: VerdictOK, Verified: 3, EvolveCount: 3, Sealed: true,
	})
	checkReport(t, Verify(es, []Checkpoint{{Seq: 0, Key: 100}, {Seq: 3, Key: 2844}}, f), Report{
		Verdict: VerdictCheckpointConflict, Pos: 3, Verified: 3, EvolveCount: 3,
	})
}

func TestTruncationCheckpointBeyondM(t *testing.T) {
	f := exampleFuncs()
	l := mustNew(t, f, 100, 10, 16)
	mustOp(t, l.Append(10, []byte("A")))
	mustOp(t, l.Append(10, []byte("B")))
	mustOp(t, l.Seal(12))
	es := l.Entries()
	checkReport(t, Verify(es, []Checkpoint{{Seq: 0, Key: 100}, {Seq: 9, Key: 1}}, f), Report{
		Verdict: VerdictTruncation, Pos: 3, Verified: 3, EvolveCount: 3,
	})
	// j0 beyond m: everything unverifiable, still truncation at m.
	checkReport(t, Verify(es, []Checkpoint{{Seq: 5, Key: 1}}, f), Report{
		Verdict: VerdictTruncation, Pos: 3, Unverifiable: 3,
	})
}

// shaFuncs builds evolve/rekey/mac from SHA-256 folded to uint64, so that
// knowing a checkpoint key does not allow deriving earlier keys.
func shaFuncs() Funcs {
	u64 := func(v uint64) []byte {
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], v)
		return b[:]
	}
	fold := func(parts ...[]byte) uint64 {
		h := sha256.New()
		for _, p := range parts {
			h.Write(p)
		}
		return binary.BigEndian.Uint64(h.Sum(nil)[:8])
	}
	return Funcs{
		Evolve: func(k uint64) uint64 { return fold([]byte("evolve"), u64(k)) },
		Rekey:  func(k, i uint64) uint64 { return fold([]byte("rekey"), u64(k), u64(i)) },
		Mac: func(k, i, typ, ts uint64, data []byte) uint64 {
			return fold([]byte("mac"), u64(k), u64(i), u64(typ), u64(ts), data)
		},
	}
}

// An attacker holding the checkpoint key at position c can rewrite entries
// c..m-1 freely and still pass verification, but any change before c is
// always detected. Uses SHA-256 based functions because the linear example
// functions are invertible.
func TestForwardSecurityAttackModel(t *testing.T) {
	f := shaFuncs()
	const k0 = 42
	l := mustNew(t, f, k0, 16, 16)
	for i := 0; i < 6; i++ {
		mustOp(t, l.Append(uint64(i+1), []byte{byte('a' + i)}))
	}
	es := l.Entries()
	keys := keyChain(f, es, k0)
	const c = 3

	// Attacker rewrites everything from position c onward.
	forged := cloneEntries(es)
	k := keys[c]
	for pos := c; pos < len(forged); pos++ {
		data := []byte("forged-" + string(rune('0'+pos)))
		ts := uint64(100 + pos)
		forged[pos] = Entry{
			Index: uint64(pos), Typ: TypData, Ts: ts, Data: data,
			Tag: f.Mac(k, uint64(pos), TypData, ts, data),
		}
		k = f.Evolve(k)
	}
	rep := Verify(forged, []Checkpoint{{Seq: c, Key: keys[c]}}, f)
	if rep.Verdict != VerdictOK || rep.Verified != 3 || rep.Unverifiable != c {
		t.Fatalf("forged tail must verify: %+v", rep)
	}
	rep = Verify(forged, []Checkpoint{{Seq: 0, Key: k0}}, f)
	if rep.Verdict != VerdictOK || rep.Verified != 6 {
		t.Fatalf("forged tail must verify from genesis too: %+v", rep)
	}

	// Any modification before c is detected.
	forged[1].Data = []byte("tampered")
	rep = Verify(forged, []Checkpoint{{Seq: c, Key: keys[c]}}, f)
	if rep.Verdict != VerdictOK {
		t.Fatalf("entry before j0 is not even read: %+v", rep)
	}
	rep = Verify(forged, []Checkpoint{{Seq: 0, Key: k0}}, f)
	if rep.Verdict != VerdictTamper || rep.Pos != 1 {
		t.Fatalf("tamper before checkpoint must be caught: %+v", rep)
	}
}
