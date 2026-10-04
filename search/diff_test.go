package search

import (
	"fmt"
	"math/rand"
	"testing"
)

// fmtKey renders a sort key for logs.
func fmtKey(k *Key) string {
	if k == nil {
		return "-"
	}
	return fmt.Sprintf("(%d,%d,%d)", k.SortVal, k.Seg, k.Idx)
}

func fmtEntries(es []Entry) string {
	if len(es) == 0 {
		return "[]"
	}
	s := "["
	for i, e := range es {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%s%s", e.ID, fmtKey(&e.Key))
	}
	return s + "]"
}

func sameErr(a, b error) bool { return classify(a) == classify(b) }

func assertReleased(t *testing.T, e *Engine, s *simulator, log []string, ctx string) {
	t.Helper()
	got := e.Released()
	want := append([]int(nil), s.released...)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		for _, l := range log {
			t.Log(l)
		}
		t.Fatalf("%s: released mismatch eng=%v sim=%v", ctx, got, want)
	}
	seen := map[int]bool{}
	for _, sid := range got {
		if seen[sid] {
			t.Fatalf("%s: released segment duplicated: %d", ctx, sid)
		}
		seen[sid] = true
	}
}

func runSequence(t *testing.T, seed, ops int, verbose bool) {
	t.Helper()
	rng := rand.New(rand.NewSource(int64(seed)))
	e := NewEngine(simPmax)
	s := newSim(rng)
	var log []string
	push := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		log = append(log, line)
		if verbose {
			t.Log(line)
		}
	}

	for i := 0; i < ops; i++ {
		kind := rng.Intn(8)
		// Keep clock mostly non-decreasing, with occasional deliberate rollbacks.
		now := s.now
		if rng.Intn(5) != 0 {
			now += int64(rng.Intn(4))
		} else if s.now > 0 {
			now -= int64(rng.Intn(2))
			if now < 0 {
				now = 0
			}
		}

		switch kind {
		case 0, 1: // add, occasionally malformed
			docs, bad := randomDocs(rng)
			simID, simErr := s.add(now, docs)
			engID, engErr := e.AddSegment(now, docs)
			why := classify(simErr)
			push("AddSegment now=%d n=%d bad=%v => sim(%d,%s) eng(%d,%s)",
				now, len(docs), bad, simID, why, engID, classify(engErr))
			if !sameErr(simErr, engErr) || (simErr == nil && simID != engID) {
				dumpAndFail(t, log, "AddSegment", simID, simErr, engID, engErr)
			}
		case 2: // delete, occasionally empty id
			id := fmt.Sprintf("d%d", rng.Intn(10))
			if rng.Intn(8) == 0 {
				id = ""
			}
			simErr := s.del(now, id)
			engErr := e.Delete(now, id)
			push("Delete now=%d id=%q => sim(%s) eng(%s)", now, id,
				classify(simErr), classify(engErr))
			if !sameErr(simErr, engErr) {
				dumpAndFail(t, log, "Delete", 0, simErr, 0, engErr)
			}
		case 3: // merge
			segs := randomMergeSegs(rng, s)
			simID, simErr := s.merge(now, segs)
			engID, engErr := e.Merge(now, segs)
			push("Merge now=%d segs=%v => sim(%d,%s) eng(%d,%s)",
				now, segs, simID, classify(simErr), engID, classify(engErr))
			if !sameErr(simErr, engErr) || (simErr == nil && simID != engID) {
				dumpAndFail(t, log, "Merge", simID, simErr, engID, engErr)
			}
		case 4: // open
			ka := int64(1 + rng.Intn(10))
			if rng.Intn(10) == 0 {
				ka = 0 // invalid ka
			}
			simID, simErr := s.open(now, ka)
			engP, engErr := e.Open(now, ka)
			push("Open now=%d ka=%d livePIT=%d => sim(%d,%s) eng(%d,%s)",
				now, ka, len(s.pits), simID, classify(simErr), engP.ID, classify(engErr))
			if !sameErr(simErr, engErr) || (simErr == nil && (simID != engP.ID ||
				s.pits[simID].exp != engP.Exp || s.pits[simID].op != engP.Op)) {
				dumpAndFail(t, log, "Open", simID, simErr, engP.ID, engErr)
			}
		case 5: // close
			pid := randomPIT(rng, s)
			simErr := s.close(now, pid)
			engErr := e.Close(now, pid)
			push("Close now=%d pid=%d => sim(%s) eng(%s)", now, pid,
				classify(simErr), classify(engErr))
			if !sameErr(simErr, engErr) {
				dumpAndFail(t, log, "Close", 0, simErr, 0, engErr)
			}
		default: // search: current view or a PIT, with paging and renewal
			size := 1 + rng.Intn(4)
			if rng.Intn(15) == 0 {
				size = 0 // invalid
			}
			var pid int
			var ka int64
			if rng.Intn(2) == 0 {
				pid = 0
			} else {
				pid = randomPIT(rng, s)
				if rng.Intn(3) == 0 {
					ka = int64(1 + rng.Intn(12))
				}
			}
			var after *Key
			if pid != 0 && rng.Intn(3) == 0 {
				after = &Key{
					SortVal: int64(rng.Intn(6)),
					Seg:     1 + rng.Intn(6),
					Idx:     rng.Intn(4),
				}
			}
			simRes, simErr := s.search(now, pid, size, after, ka)
			engRes, engErr := e.Search(now, pid, size, after, ka)
			push("Search now=%d pid=%d size=%d after=%s ka=%d => sim(%s,%s) eng(%s,%s)",
				now, pid, size, fmtKey(after), ka,
				classify(simErr), fmtEntries(simRes), classify(engErr), fmtEntries(engRes))
			if !sameErr(simErr, engErr) {
				dumpAndFail(t, log, "Search", 0, simErr, 0, engErr)
			}
			if simErr == nil && fmtEntries(simRes) != fmtEntries(engRes) {
				dumpAndFail(t, log, "SearchResults", 0, nil, 0, nil)
			}
			// Whenever a PIT search succeeded with renewal, exps must agree.
			if simErr == nil && pid != 0 {
				gotP, ok := lookupPIT(e, pid)
				if !ok || gotP != s.pits[pid].exp {
					for _, l := range log {
						t.Log(l)
					}
					t.Fatalf("exp mismatch pid=%d eng=%v sim=%d", pid, gotP, s.pits[pid].exp)
				}
			}
		}
		assertReleased(t, e, s, log, fmt.Sprintf("seed=%d step=%d", seed, i))
	}

	// Paging equivalence for every surviving PIT and for the current view.
	for pid := range s.pits {
		full, err := s.search(s.now, pid, 1000, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, size := range []int{1, 2, 3, 5} {
			var stitched []Entry
			var after *Key
			for {
				page, perr := e.Search(s.now, pid, size, after, 0)
				if perr != nil {
					t.Fatalf("paging pid=%d size=%d: %v", pid, size, perr)
				}
				stitched = append(stitched, page...)
				if len(page) < size {
					break
				}
				k := page[len(page)-1].Key
				after = &k
			}
			if fmtEntries(stitched) != fmtEntries(full) {
				t.Fatalf("seed=%d paging pid=%d size=%d stitched=%s full=%s",
					seed, pid, size, fmtEntries(stitched), fmtEntries(full))
			}
		}
	}
	curSim, _ := s.search(s.now, 0, 1000, nil, 0)
	curEng, err := e.Search(s.now, 0, 1000, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if fmtEntries(curSim) != fmtEntries(curEng) {
		t.Fatalf("seed=%d current view mismatch sim=%s eng=%s",
			seed, fmtEntries(curSim), fmtEntries(curEng))
	}
}

func randomDocs(rng *rand.Rand) ([]Doc, bool) {
	if rng.Intn(12) == 0 {
		return nil, true // invalid empty batch
	}
	n := 1 + rng.Intn(5)
	docs := make([]Doc, 0, n)
	used := map[string]bool{}
	for len(docs) < n {
		var id string
		if rng.Intn(15) == 0 {
			id = "" // invalid empty id
		} else {
			id = fmt.Sprintf("d%d", rng.Intn(10))
		}
		if used[id] {
			continue
		}
		used[id] = true
		docs = append(docs, dd(id, int64(rng.Intn(5))))
	}
	if rng.Intn(10) == 0 && len(docs) > 1 {
		docs[1].ID = docs[0].ID // duplicate in batch
		return docs, true
	}
	return docs, false
}

func randomMergeSegs(rng *rand.Rand, s *simulator) []int {
	var alive []int
	for id, sg := range s.segs {
		if sg.alive {
			alive = append(alive, id)
		}
	}
	sortIntsAsc(alive)
	if len(alive) >= 2 && rng.Intn(4) != 0 {
		rng.Shuffle(len(alive), func(i, j int) { alive[i], alive[j] = alive[j], alive[i] })
		n := 2 + rng.Intn(len(alive)-1)
		if n > 10 {
			n = 10
		}
		segs := append([]int(nil), alive[:n]...)
		if rng.Intn(8) == 0 {
			segs[1] = segs[0] // duplicate -> invalid
		}
		return segs
	}
	// Otherwise generate a likely-invalid pair (wrong count / missing / dup).
	switch rng.Intn(3) {
	case 0:
		return []int{1 + rng.Intn(8)}
	case 1:
		return []int{1 + rng.Intn(8), 1 + rng.Intn(8)}
	default:
		x := 1 + rng.Intn(8)
		return []int{x, x}
	}
}

func randomPIT(rng *rand.Rand, s *simulator) int {
	if len(s.pits) > 0 && rng.Intn(3) != 0 {
		i := rng.Intn(len(s.pits))
		for id := range s.pits {
			if i == 0 {
				return id
			}
			i--
		}
	}
	return 1 + rng.Intn(5)
}

func sortIntsAsc(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

func lookupPIT(e *Engine, pid int) (int64, bool) {
	for _, p := range e.Manager().PITs() {
		if p.ID == pid {
			return p.Exp, true
		}
	}
	return 0, false
}

func dumpAndFail(t *testing.T, log []string, op string, simID int, simErr error, engID int, engErr error) {
	t.Helper()
	for _, l := range log {
		t.Log(l)
	}
	t.Fatalf("%s mismatch: sim(%d,%v) eng(%d,%v)", op, simID, simErr, engID, engErr)
}

func TestRandomDifferential(t *testing.T) {
	const sequences = 1500
	const opsPerSeq = 60
	for seed := 1; seed <= sequences; seed++ {
		runSequence(t, seed, opsPerSeq, seed <= 5)
	}
	t.Logf("differential test: %d sequences x %d ops all matched naive simulator",
		sequences, opsPerSeq)
}

func TestRandomDifferentialSingleVerbose(t *testing.T) {
	runSequence(t, 1, 40, true)
}
