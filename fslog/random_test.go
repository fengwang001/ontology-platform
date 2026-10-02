package fslog

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

func TestRejectedOpsKeepState(t *testing.T) {
	f := exampleFuncs()
	l := mustNew(t, f, 100, 4, 2)
	mustOp(t, l.Append(10, []byte("A")))

	snap := l.Entries()
	i0, k0, err := l.Export()
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	assertState := func(stage string) {
		t.Helper()
		if got := l.Entries(); !reflect.DeepEqual(got, snap) {
			t.Fatalf("%s: entries changed by rejected op: %v", stage, got)
		}
		i, k, err := l.Export()
		if err != nil || i != i0 || k != k0 {
			t.Fatalf("%s: export changed to (%d,%d,%v)", stage, i, k, err)
		}
	}
	if opKind(l.Append(MaxTs+1, nil)) != OpInvalidArg {
		t.Fatal("want invalid-arg")
	}
	assertState("invalid ts")
	if opKind(l.Append(10, []byte("too-long"))) != OpInvalidArg {
		t.Fatal("want invalid-arg")
	}
	assertState("data too long")
	if opKind(l.Append(9, nil)) != OpTimeRegression {
		t.Fatal("want time-regression")
	}
	assertState("time regression")
	if opKind(l.Rekey(9)) != OpTimeRegression {
		t.Fatal("want time-regression")
	}
	assertState("rekey time regression")
	if opKind(l.Seal(9)) != OpTimeRegression {
		t.Fatal("want time-regression")
	}
	assertState("seal time regression")

	mustOp(t, l.Append(10, []byte("B")))
	mustOp(t, l.Rekey(10))
	snap = l.Entries()
	i0, k0, _ = l.Export()
	if opKind(l.Append(10, nil)) != OpCapacityFull {
		t.Fatal("want capacity-full")
	}
	assertState("capacity full")
	if opKind(l.Rekey(10)) != OpCapacityFull {
		t.Fatal("want capacity-full")
	}
	assertState("rekey capacity full")

	mustOp(t, l.Seal(10))
	snap = l.Entries()
	if opKind(l.Append(11, nil)) != OpSealed {
		t.Fatal("want sealed")
	}
	if got := l.Entries(); !reflect.DeepEqual(got, snap) {
		t.Fatalf("entries changed after seal rejection: %v", got)
	}
}

func TestDeterministicReplay(t *testing.T) {
	f := exampleFuncs()
	build := func() *Log {
		l := mustNew(t, f, 100, 16, 8)
		mustOp(t, l.Append(3, []byte("x")))
		mustOp(t, l.Rekey(4))
		mustOp(t, l.Append(4, []byte("y")))
		mustOp(t, l.Seal(5))
		return l
	}
	a, b := build(), build()
	if !reflect.DeepEqual(a.Entries(), b.Entries()) {
		t.Fatal("identical op sequences must produce identical entries")
	}
	ra := a.SelfVerify([]Checkpoint{{Seq: 0, Key: 100}})
	rb := b.SelfVerify([]Checkpoint{{Seq: 0, Key: 100}})
	if ra != rb {
		t.Fatal("identical op sequences must produce identical reports")
	}
}

func TestConcurrentOps(t *testing.T) {
	f := exampleFuncs()
	const writers = 8
	const perWriter = 50
	l := mustNew(t, f, 100, writers*perWriter+1, 4)
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < perWriter; n++ {
				if err := l.Append(1, []byte("x")); err != nil {
					t.Errorf("append: %v", err)
					return
				}
				_ = l.Entries()
				_, _, _ = l.Export()
				l.SelfVerify([]Checkpoint{{Seq: 0, Key: 100}})
			}
		}()
	}
	wg.Wait()
	i, k, err := l.Export()
	if err != nil || i != writers*perWriter {
		t.Fatalf("export = (%d,%d,%v), want i=%d", i, k, err, writers*perWriter)
	}
	rep := l.SelfVerify([]Checkpoint{{Seq: 0, Key: 100}})
	if rep.Verdict != VerdictOK || rep.Verified != writers*perWriter {
		t.Fatalf("self-verify after concurrent writes: %+v", rep)
	}
	// A checkpoint exported mid-run must be consistent with a prefix of the log.
	rep = l.SelfVerify([]Checkpoint{{Seq: int64(i), Key: k}})
	if rep.Verdict != VerdictOK || rep.Unverifiable != uint64(writers*perWriter) {
		t.Fatalf("self-verify from exported checkpoint: %+v", rep)
	}
}

// naiveVerify is the deliberately naive reference: for every position and
// every checkpoint it re-derives the key from scratch starting at the j0
// checkpoint, instead of evolving once in a single pass.
func naiveVerify(entries []Entry, cps []Checkpoint, f Funcs) Report {
	if len(cps) == 0 {
		return Report{Verdict: VerdictInvalidArg}
	}
	keys := make(map[int64]uint64, len(cps))
	seqs := make([]int64, 0, len(cps))
	for _, cp := range cps {
		if cp.Seq < 0 {
			return Report{Verdict: VerdictInvalidArg}
		}
		if _, dup := keys[cp.Seq]; dup {
			return Report{Verdict: VerdictInvalidArg}
		}
		keys[cp.Seq] = cp.Key
		seqs = append(seqs, cp.Seq)
	}
	sort.Slice(seqs, func(a, b int) bool { return seqs[a] < seqs[b] })
	m := uint64(len(entries))
	j0 := uint64(seqs[0])
	var rep Report
	rep.Unverifiable = min(j0, m)

	// keyAt walks the whole chain from j0 for every single query.
	keyAt := func(t uint64) uint64 {
		k := keys[seqs[0]]
		for s := j0; s < t; s++ {
			if entries[s].Typ == TypRekey {
				k = f.Rekey(k, s)
			} else {
				k = f.Evolve(k)
			}
		}
		return k
	}

	for t := j0; t < m; t++ {
		k := keyAt(t)
		if t != j0 {
			if ck, ok := keys[int64(t)]; ok && ck != k {
				rep.Verdict = VerdictCheckpointConflict
				rep.Pos = t
				return rep
			}
		}
		e := entries[t]
		switch {
		case e.Index > t:
			rep.Verdict = VerdictGap
			rep.Pos = t
			return rep
		case e.Index < t:
			rep.Verdict = VerdictReplay
			rep.Pos = t
			return rep
		}
		if t > j0 {
			prev := entries[t-1]
			if prev.Typ == TypSeal {
				rep.Verdict = VerdictAppendAfterSeal
				rep.Pos = t
				return rep
			}
			if e.Ts < prev.Ts {
				rep.Verdict = VerdictTimeRegression
				rep.Pos = t
				return rep
			}
		}
		if e.Typ > TypRekey {
			rep.Verdict = VerdictTamper
			rep.Pos = t
			return rep
		}
		if e.Tag != f.Mac(k, t, e.Typ, e.Ts, e.Data) {
			rep.Verdict = VerdictTamper
			rep.Pos = t
			return rep
		}
		if e.Typ == TypSeal || e.Typ == TypRekey {
			if string(e.Data) != fmt.Sprintf("%d", t) {
				rep.Verdict = VerdictContentMismatch
				rep.Pos = t
				return rep
			}
		}
		rep.Verified++
		if e.Typ == TypRekey {
			rep.RekeyCount++
		} else {
			rep.EvolveCount++
		}
	}
	if ck, ok := keys[int64(m)]; ok && ck != keyAt(m) {
		rep.Verdict = VerdictCheckpointConflict
		rep.Pos = m
		return rep
	}
	for _, s := range seqs {
		if uint64(s) > m {
			rep.Verdict = VerdictTruncation
			rep.Pos = m
			return rep
		}
	}
	rep.Verdict = VerdictOK
	rep.Sealed = m > 0 && j0 <= m-1 && entries[m-1].Typ == TypSeal
	return rep
}

func summarizeEntries(es []Entry) string {
	s := "["
	for i, e := range es {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("{i:%d typ:%d ts:%d data:%q tag:%d}", e.Index, e.Typ, e.Ts, e.Data, e.Tag)
	}
	return s + "]"
}

// TestRandomizedVsNaive replays 2000 random write/tamper/truncate/verify
// sequences and compares the one-pass verifier against the naive simulation
// on verdict category, position and verified count.
func TestRandomizedVsNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	f := exampleFuncs()
	for iter := 0; iter < 2000; iter++ {
		capacity := uint64(2 + rng.Intn(12))
		k0 := uint64(rng.Intn(1000))
		l := mustNew(t, f, k0, capacity, 4)

		ts := uint64(rng.Intn(5))
		ops := 1 + rng.Intn(12)
		for op := 0; op < ops; op++ {
			ts += uint64(rng.Intn(3))
			switch rng.Intn(4) {
			case 0, 1:
				_ = l.Append(ts, []byte{byte('a' + rng.Intn(26))})
			case 2:
				_ = l.Rekey(ts)
			case 3:
				_ = l.Seal(ts)
			}
		}
		entries := l.Entries()

		// Random mutations: tamper, truncate, duplicate, drop.
		mutations := rng.Intn(3)
		for mu := 0; mu < mutations && len(entries) > 0; mu++ {
			pos := rng.Intn(len(entries))
			switch rng.Intn(11) {
			case 0:
				entries[pos].Data = []byte("mut")
			case 1:
				entries[pos].Tag++
			case 2:
				entries[pos].Ts = uint64(rng.Intn(20))
			case 3:
				entries[pos].Index = uint64(rng.Intn(len(entries) + 2))
			case 4:
				entries[pos].Typ = uint64(rng.Intn(6))
			case 5: // truncate tail
				entries = entries[:rng.Intn(len(entries)+1)]
			case 6: // duplicate an entry at the end (replay)
				entries = append(entries, entries[pos])
			case 7: // drop an entry (gap)
				entries = append(entries[:pos], entries[pos+1:]...)
			case 8: // content mismatch with a valid tag
				chain := keyChain(f, entries, k0)
				typ := uint64(1 + rng.Intn(2))
				entries[pos].Typ = typ
				entries[pos].Data = []byte("bad")
				entries[pos].Tag = f.Mac(chain[pos], uint64(pos), typ, entries[pos].Ts, entries[pos].Data)
			case 9: // continuation entry after a trailing seal
				if last := entries[len(entries)-1]; last.Typ == TypSeal {
					entries = append(entries, Entry{
						Index: uint64(len(entries)), Typ: TypData,
						Ts: last.Ts, Data: []byte("z"),
					})
				}
			case 10: // time regression with a valid tag
				if pos > 0 && entries[pos-1].Ts > 0 {
					chain := keyChain(f, entries, k0)
					entries[pos].Ts = entries[pos-1].Ts - 1
					entries[pos].Tag = f.Mac(chain[pos], uint64(pos), entries[pos].Typ, entries[pos].Ts, entries[pos].Data)
				}
			}
		}

		// Checkpoints: 1..3 distinct seqs, keys mostly derived from the chain.
		chain := keyChain(f, entries, k0)
		m := len(entries)
		nCps := 1 + rng.Intn(3)
		used := map[int64]bool{}
		var cps []Checkpoint
		for len(cps) < nCps {
			seq := int64(rng.Intn(m + 3))
			if used[seq] {
				continue
			}
			used[seq] = true
			var key uint64
			if seq <= int64(m) && rng.Intn(4) > 0 {
				key = chain[seq]
			} else {
				key = uint64(rng.Intn(1000))
			}
			cps = append(cps, Checkpoint{Seq: seq, Key: key})
		}

		got := Verify(entries, cps, f)
		want := naiveVerify(entries, cps, f)
		reason := fmt.Sprintf("first failing check in fixed order at pos %d", got.Pos)
		if got.Verdict == VerdictOK {
			reason = "all checks passed"
		}
		t.Logf("iter %d: input entries=%s cps=%+v output=%+v naive=%+v reason=%s",
			iter, summarizeEntries(entries), cps, got, want, reason)
		if got != want {
			t.Fatalf("iter %d mismatch:\nentries=%s\ncps=%+v\n got %+v\nwant %+v",
				iter, summarizeEntries(entries), cps, got, want)
		}
	}
}
