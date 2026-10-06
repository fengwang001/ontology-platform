package store

import "testing"

func TestReadWriteBehaviorDuringCatchUp(t *testing.T) {
	records := recoveryTestRecords()
	s := recoveredStoreFromRecords(t, records, 2, 2)

	_, _, err := s.Find("a")
	requireErrorIs(t, err, ErrIndexBehind)
	_, err = s.Check()
	requireErrorIs(t, err, ErrIndexBehind)

	row, err := s.Get("p1")
	requireNoError(t, err)
	requireNilSecondary(t, row)

	_, err = s.Put(Row{Primary: "p9", Secondary: strPtr("a")})
	requireErrorIs(t, err, ErrUniqueConflict)
	if last := s.LastLSN(); last != int64(len(records)) {
		t.Fatalf("last LSN after rejected write = %d, want %d", last, len(records))
	}

	lsn, err := s.Put(Row{Primary: "p9", Secondary: strPtr("x")})
	requireNoError(t, err)
	if lsn != int64(len(records))+1 {
		t.Fatalf("accepted LSN = %d, want %d", lsn, len(records)+1)
	}

	advanced, err := s.CatchUp(3)
	requireNoError(t, err)
	if advanced != 5 {
		t.Fatalf("advanced = %d, want 5", advanced)
	}
	for advanced < lsn {
		advanced, err = s.CatchUp(3)
		requireNoError(t, err)
	}

	primary, at, err := s.Find("x")
	requireNoError(t, err)
	if primary != "p9" || at != lsn {
		t.Fatalf("find x = (%q,%d), want (p9,%d)", primary, at, lsn)
	}
	primary, _, err = s.Find("a")
	requireNoError(t, err)
	if primary != "p2" {
		t.Fatalf("find a = %q, want p2", primary)
	}
	report, err := s.Check()
	requireNoError(t, err)
	if len(report.Mismatches) != 0 {
		t.Fatalf("check mismatches = %#v", report.Mismatches)
	}
}
