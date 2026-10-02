package tablespace

import (
	"fmt"
	"math/rand"
	"testing"
)

type opKind int

const (
	opNew opKind = iota
	opAlloc
	opFree
	opFreeSeg
	opUsed
)

type testOp struct {
	kind opKind
	s    int
	hint int
	page int
}

func errKind(err error) string {
	switch err {
	case nil:
		return ""
	case ErrInvalidArgument:
		return "invalid"
	case ErrSegmentNotFound:
		return "noseg"
	case ErrPageNotOwned:
		return "notowned"
	case ErrNoSpace:
		return "nospace"
	default:
		return "other:" + err.Error()
	}
}

func statesEqual(a *Allocator, m *naiveModel) string {
	snap := a.Snapshot()
	for eid := 0; eid < m.e; eid++ {
		want := m.state[eid]
		if want == "SEG" {
			want = fmt.Sprintf("SEG(%d)", m.owner[eid])
		}
		if snap.Extents[eid].State != want {
			return fmt.Sprintf("extent %d state: impl=%s naive=%s", eid, snap.Extents[eid].State, want)
		}
		if snap.Extents[eid].Used != m.used[eid] {
			return fmt.Sprintf("extent %d used: impl=%d naive=%d", eid, snap.Extents[eid].Used, m.used[eid])
		}
	}
	for p := 0; p < m.e*m.x; p++ {
		if snap.Pages[p] != m.pageOwner[p] {
			return fmt.Sprintf("page %d owner: impl=%d naive=%d", p, snap.Pages[p], m.pageOwner[p])
		}
	}
	for s := 1; s < m.nextID; s++ {
		if m.alive[s] != snap.Alive[s] {
			return fmt.Sprintf("segment %d alive: impl=%v naive=%v", s, snap.Alive[s], m.alive[s])
		}
		if !m.alive[s] {
			continue
		}
		if snap.Used[s] != m.segUsed[s] {
			return fmt.Sprintf("segment %d used: impl=%d naive=%d", s, snap.Used[s], m.segUsed[s])
		}
		nq := snap.NonFullQueues[s]
		mq := m.queue[s]
		if len(nq) != len(mq) {
			return fmt.Sprintf("segment %d queue len: impl=%v naive=%v", s, nq, mq)
		}
		for i := range nq {
			if nq[i] != mq[i] {
				return fmt.Sprintf("segment %d queue order: impl=%v naive=%v", s, nq, mq)
			}
		}
	}
	if snap.NextSegID != m.nextID {
		return fmt.Sprintf("nextID impl=%d naive=%d", snap.NextSegID, m.nextID)
	}
	return ""
}

func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		x := 2 + rng.Intn(7)       // 2..8
		f := 1 + rng.Intn(x)       // 1..x
		e := 1 + rng.Intn(5)       // 1..5 extents
		steps := 15 + rng.Intn(20) // 15..34 ops

		a, err := New(x, f, e)
		if err != nil {
			t.Fatalf("seq %d: New: %v", seq, err)
		}
		m := newNaive(x, f, e)

		var log []string
		log = append(log, fmt.Sprintf("seq=%d X=%d F=%d E=%d", seq, x, f, e))

		nextS := 1
		compare := func(stage string) {
			t.Helper()
			if diff := statesEqual(a, m); diff != "" {
				for _, line := range log {
					t.Log(line)
				}
				t.Fatalf("seq %d mismatch after %s: %s", seq, stage, diff)
			}
			if err := a.CheckInvariants(); err != nil {
				for _, line := range log {
					t.Log(line)
				}
				t.Fatalf("seq %d invariant broken after %s: %v", seq, stage, err)
			}
		}

		for st := 0; st < steps; st++ {
			// Occasionally pass invalid arguments on purpose.
			r := rng.Float64()
			switch {
			case r < 0.15 || nextS == 1:
				id := a.NewSegment()
				mid := m.newSegment()
				log = append(log, fmt.Sprintf("step %d NewSegment -> impl=%d naive=%d", st, id, mid))
				if id != mid {
					t.Fatalf("NewSegment id mismatch %d != %d", id, mid)
				}
				nextS = id + 1
			case r < 0.55:
				s := 1 + rng.Intn(nextS) // includes dead ids for rejection tests
				hint := -1
				if rng.Intn(3) == 0 {
					hint = rng.Intn(e*x + 2) // some out of range
					if rng.Intn(10) == 0 {
						hint = -2
					}
				}
				page, err := a.AllocPage(s, hint)
				mpage, ek := m.alloc(s, hint)
				reason := fmt.Sprintf("impl=(%d,%s) naive=(%d,%s)", page, errKind(err), mpage, ek)
				log = append(log, fmt.Sprintf("step %d AllocPage(s=%d,hint=%d) -> %s", st, s, hint, reason))
				if page != mpage || errKind(err) != ek {
					for _, line := range log {
						t.Log(line)
					}
					t.Fatalf("seq %d step %d alloc divergence", seq, st)
				}
				if ek == "" {
					// Determination basis: fragment vs exclusive mode.
					basis := "fragment"
					if m.segUsed[s]-1 >= f {
						basis = "exclusive"
					}
					log[len(log)-1] += "  // mode=" + basis + " extent=" + fmt.Sprint(page/x)
				}
				compare("alloc")
			case r < 0.9:
				s := 1 + rng.Intn(nextS)
				page := rng.Intn(e*x + 1) // some out of range
				err := a.FreePage(s, page)
				ek := m.free(s, page)
				log = append(log, fmt.Sprintf("step %d FreePage(s=%d,p=%d) -> impl=%s naive=%s", st, s, page, errKind(err), ek))
				if errKind(err) != ek {
					for _, line := range log {
						t.Log(line)
					}
					t.Fatalf("seq %d step %d free divergence", seq, st)
				}
				compare("free")
			default:
				s := 1 + rng.Intn(nextS)
				err := a.FreeSegment(s)
				ek := m.freeSegment(s)
				log = append(log, fmt.Sprintf("step %d FreeSegment(s=%d) -> impl=%s naive=%s", st, s, errKind(err), ek))
				if errKind(err) != ek {
					for _, line := range log {
						t.Log(line)
					}
					t.Fatalf("seq %d step %d freeseg divergence", seq, st)
				}
				compare("freeseg")
			}
		}
		// Print one sample log per 500 sequences so the basis logging is
		// visible without flooding the output.
		if seq%500 == 0 {
			for _, line := range log {
				t.Log(line)
			}
		}
	}
}
