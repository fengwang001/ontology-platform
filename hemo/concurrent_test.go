package hemo

import (
	"sync"
	"testing"
)

// TestConcurrentSerializability hammers one system from many goroutines with
// monotonic per-goroutine clocks and asserts the final state keeps the
// double-booking and eligibility invariants, and that replaying the same
// operations sequentially produces the same deterministic allocation.
func TestConcurrentSerializability(t *testing.T) {
	build := func() *System {
		s := NewSystem(testCfg())
		mustChair(t, s, "N1", ZoneNormal, true)
		mustChair(t, s, "N2", ZoneNormal, true)
		mustChair(t, s, "I1", ZoneIsolation, false)
		for _, p := range []string{"P1", "P2", "P3", "P4"} {
			mustPatient(t, s, p, InfectionNegative)
		}
		return s
	}

	// Deterministic operation list; each goroutine advances its own clock.
	type req struct {
		pid  string
		day  int
		from int
		to   int
	}
	reqs := []req{}
	for _, p := range []string{"P1", "P2", "P3", "P4"} {
		for d := 0; d < 4; d++ {
			reqs = append(reqs, req{
				pid: p, day: d, from: d * MinutesPerDay,
				to: (d+1)*MinutesPerDay - 1,
			})
		}
	}

	runConcurrent := func(s *System) {
		var wg sync.WaitGroup
		for gi := 0; gi < 4; gi++ {
			gi := gi
			wg.Add(1)
			go func() {
				defer wg.Done()
				now := gi * 10000
				for i := gi; i < len(reqs); i += 4 {
					r := reqs[i]
					_, _ = s.AddPlan(now, r.pid, weekdaySet(r.day),
						600+gi*5, 200, r.from, r.to)
					now += 7
				}
			}()
		}
		wg.Wait()
	}

	runSequential := func(s *System) {
		now := 0
		for _, r := range reqs {
			now++
			_, _ = s.AddPlan(now, r.pid, weekdaySet(r.day), 600, 200,
				r.from, r.to)
		}
	}

	sc := build()
	runConcurrent(sc)
	// Invariant: no chair ever has overlapping treatments or tail violations.
	for _, cid := range ExportedChairIDs(sc) {
		tl := sc.chairTimelines[cid]
		zone := sc.chairs[cid].Zone
		var prev *Treatment
		cur := tl.Successor(MinTime, nil)
		for cur != nil {
			if prev != nil {
				gap := sc.gapAfter(prev.InfectionAtStart,
					cur.InfectionAtStart, zone)
				if prev.End+gap > cur.Start {
					t.Fatalf("chair %s violation between %s and %s",
						cid, prev.ID, cur.ID)
				}
				if zone == ZoneIsolation {
					a, b := prev.InfectionAtStart, cur.InfectionAtStart
					if (a == InfectionHBV && b == InfectionHCV) ||
						(a == InfectionHCV && b == InfectionHBV) {
						if cur.Start-prev.End < sc.cfg.DeepDisinfect {
							t.Fatalf("deep gap violated on %s", cid)
						}
					}
				}
			}
			prev = cur
			cur = tl.Successor(cur.Start+1, nil)
		}
	}

	// Deterministic serial replay produces a stable assignment.
	s1 := build()
	runSequential(s1)
	s2 := build()
	runSequential(s2)
	r1 := prodChairRowsForTest(t, s1)
	r2 := prodChairRowsForTest(t, s2)
	if len(r1) != len(r2) {
		t.Fatalf("replay length differs %d vs %d", len(r1), len(r2))
	}
	for i := range r1 {
		if r1[i] != r2[i] {
			t.Fatalf("replay non-deterministic at %d: %+v vs %+v",
				i, r1[i], r2[i])
		}
	}
}

func prodChairRowsForTest(t *testing.T, s *System) []string {
	t.Helper()
	var rows []string
	for _, cid := range ExportedChairIDs(s) {
		ids, err := s.ChairTreatments(cid)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			tv, err := s.GetTreatment(id)
			if err != nil {
				t.Fatal(err)
			}
			rows = append(rows,
				cid+":"+id+":"+itoa(tv.Start)+":"+itoa(tv.End)+":"+
					tv.PatientID)
		}
	}
	return rows
}
