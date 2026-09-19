package ontology

import "testing"

// Normal traversal across exactly three pages shows every element once.
func TestPaginationThreePagesNoRepeatsNoGaps(t *testing.T) {
	s := seedStore(9)

	p1, err := s.Scan("", 3)
	if err != nil {
		t.Fatal(err)
	}
	assertKeys(t, keysOf(p1), "k00", "k01", "k02")
	if !p1.HasMore || !p1.Truncated || p1.Changed != ChangeNone {
		t.Fatalf("page1 flags wrong: %+v", p1)
	}

	p2, err := s.Scan(p1.Next, 3)
	if err != nil {
		t.Fatal(err)
	}
	assertKeys(t, keysOf(p2), "k03", "k04", "k05")
	if !p2.HasMore || !p2.Truncated {
		t.Fatalf("page2 flags wrong: %+v", p2)
	}

	p3, err := s.Scan(p2.Next, 3)
	if err != nil {
		t.Fatal(err)
	}
	assertKeys(t, keysOf(p3), "k06", "k07", "k08")
	if p3.HasMore || p3.Truncated {
		t.Fatalf("last page must not have more: %+v", p3)
	}

	// The returned end cursor rescans as an empty terminal page.
	p4, err := s.Scan(p3.Next, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(p4.Items) != 0 || p4.HasMore {
		t.Fatalf("expected empty terminal page, got %+v", p4)
	}
}

// limit larger than the remainder returns everything with hasMore=false.
func TestLimitLargerThanRemainder(t *testing.T) {
	s := seedStore(3)
	p, err := s.Scan("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 3 || p.HasMore || p.Truncated {
		t.Fatalf("bad oversized page: %+v", p)
	}
}

// limit equal to the remainder returns hasMore=false with no empty followup.
func TestLimitEqualToRemainder(t *testing.T) {
	s := seedStore(10)
	p1, _ := s.Scan("", 4)
	p2, err := s.Scan(p1.Next, 6) // exactly the remainder
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.Items) != 6 || p2.HasMore || p2.Truncated {
		t.Fatalf("exact-remainder page wrong: %+v", p2)
	}
}

// limit=0 returns an empty page and the cursor does not advance.
func TestLimitZeroDoesNotAdvance(t *testing.T) {
	s := seedStore(5)
	p0, err := s.Scan("", 0)
	if err != nil {
		t.Fatalf("limit 0 must not error: %v", err)
	}
	if len(p0.Items) != 0 || p0.Next == "" || !p0.HasMore {
		t.Fatalf("limit0 page wrong: %+v", p0)
	}
	// Next call sees the full first page: no advance, no consumption.
	p1, err := s.Scan(p0.Next, 2)
	if err != nil {
		t.Fatal(err)
	}
	assertKeys(t, keysOf(p1), "k00", "k01")

	// limit=0 mid-traversal holds position too.
	ph, err := s.Scan(p1.Next, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ph.Items) != 0 {
		t.Fatalf("limit0 mid-traversal must be empty: %+v", ph)
	}
	p2, _ := s.Scan(ph.Next, 2)
	assertKeys(t, keysOf(p2), "k02", "k03")
}

// limit<0 is an error, distinguishable from an empty page.
func TestLimitNegativeIsError(t *testing.T) {
	s := seedStore(2)
	p, err := s.Scan("", -1)
	if err != ErrInvalidLimit {
		t.Fatalf("want ErrInvalidLimit, got %v", err)
	}
	if p != nil {
		t.Fatalf("negative limit must return nil page, got %+v", p)
	}
	// Negative limit on a valid cursor still errors.
	ok, _ := s.Scan("", 1)
	if _, err := s.Scan(ok.Next, -7); err != ErrInvalidLimit {
		t.Fatalf("want ErrInvalidLimit, got %v", err)
	}
}

// Empty store: first scan is an empty terminal page, no error.
func TestEmptyStore(t *testing.T) {
	s := NewStore()
	p, err := s.Scan("", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 0 || p.HasMore {
		t.Fatalf("empty store page wrong: %+v", p)
	}
	if got := drain(t, seedStore(25), 4); len(got) != 25 {
		t.Fatalf("drain length = %d", len(got))
	}
}
