package store

import "testing"

func TestUniqueWritesAndPrimaryReads(t *testing.T) {
	s := New()
	lsn, err := s.Put(Row{Primary: "p1", Secondary: strPtr("a")})
	requireNoError(t, err)
	if lsn != 1 {
		t.Fatalf("lsn = %d, want 1", lsn)
	}

	_, err = s.Put(Row{Primary: "p2", Secondary: strPtr("a")})
	requireErrorIs(t, err, ErrUniqueConflict)
	if got := s.LastLSN(); got != 1 {
		t.Fatalf("last lsn after rejected write = %d, want 1", got)
	}

	lsn, err = s.Put(Row{Primary: "p1", Secondary: strPtr("a")})
	requireNoError(t, err)
	if lsn != 2 {
		t.Fatalf("same-owner lsn = %d, want 2", lsn)
	}

	row, err := s.Get("p1")
	requireNoError(t, err)
	requireSecondary(t, row, "a")

	primary, at, err := s.Find("a")
	requireNoError(t, err)
	if primary != "p1" || at != 2 {
		t.Fatalf("find = (%q,%d), want (p1,2)", primary, at)
	}
}

func TestSecondaryMovesReleasesAndClears(t *testing.T) {
	s := New()
	_, err := s.Put(Row{Primary: "p1", Secondary: strPtr("a")})
	requireNoError(t, err)
	_, err = s.Put(Row{Primary: "p1", Secondary: strPtr("b")})
	requireNoError(t, err)
	_, err = s.Put(Row{Primary: "p2", Secondary: strPtr("a")})
	requireNoError(t, err)
	_, err = s.Put(Row{Primary: "p2", Secondary: nil})
	requireNoError(t, err)
	_, err = s.Put(Row{Primary: "p3", Secondary: strPtr("a")})
	requireNoError(t, err)
	_, err = s.Put(Row{Primary: "p3", Secondary: nil})
	requireNoError(t, err)

	_, err = s.Put(Row{Primary: "p4", Secondary: strPtr("b")})
	requireErrorIs(t, err, ErrUniqueConflict)
	_, err = s.Delete("p1")
	requireNoError(t, err)
	_, err = s.Put(Row{Primary: "p1", Secondary: strPtr("b")})
	requireNoError(t, err)

	report, err := s.Check()
	requireNoError(t, err)
	if len(report.Mismatches) != 0 {
		t.Fatalf("check mismatches = %#v", report.Mismatches)
	}
}

func TestDeleteMissingAndInvalidArguments(t *testing.T) {
	s := New()
	_, err := s.Get("missing")
	requireErrorIs(t, err, ErrPrimaryNotFound)
	_, err = s.Delete("missing")
	requireErrorIs(t, err, ErrPrimaryNotFound)
	_, err = s.Put(Row{Primary: "", Secondary: strPtr("a")})
	requireErrorIs(t, err, ErrInvalidArgument)
	_, _, err = s.Find("")
	requireErrorIs(t, err, ErrInvalidArgument)
	_, err = s.CatchUp(-1)
	requireErrorIs(t, err, ErrInvalidArgument)
}
