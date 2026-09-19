package ontology

import (
	"errors"
	"strings"
	"testing"
)

func someCursor(t *testing.T, s *Store) string {
	t.Helper()
	p, err := s.Scan("", 2)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return p.Next
}

func TestTamperedCursorRejected(t *testing.T) {
	s, _ := seed(t, 5)
	c := someCursor(t, s)
	raw := []byte(c)
	if raw[10] == 'A' {
		raw[10] = 'B'
	} else {
		raw[10] = 'A'
	}
	_, err := s.Scan(string(raw), 2)
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("tampered cursor: err = %v, want ErrInvalidCursor", err)
	}
	if errors.Is(err, ErrInvalidSession) {
		t.Fatalf("tampered cursor must not be a session error: %v", err)
	}
}

func TestTruncatedCursorRejected(t *testing.T) {
	s, _ := seed(t, 5)
	c := someCursor(t, s)
	for _, cut := range []int{1, len(c) / 2, len(c) - 1} {
		if _, err := s.Scan(c[:cut], 2); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("truncated to %d: err = %v, want ErrInvalidCursor", cut, err)
		}
	}
}

func TestForgedCursorRejected(t *testing.T) {
	s, _ := seed(t, 5)
	for _, fake := range []string{
		"AAAA",
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", // 44 chars, bad tag
		"not-base64!!!",
		strings.Repeat("A", 60),
	} {
		if _, err := s.Scan(fake, 2); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("forged %q: err = %v, want ErrInvalidCursor", fake, err)
		}
	}
}

func TestInvalidatedSessionCursorRejected(t *testing.T) {
	s, _ := seed(t, 5)
	c := someCursor(t, s)
	if err := s.Invalidate(c); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	_, err := s.Scan(c, 2)
	if !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("invalidated cursor: err = %v, want ErrInvalidSession", err)
	}
	if errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("session error must be distinct from cursor format error: %v", err)
	}
	if _, err := s.TraversalStats(c); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("stats on invalidated session: err = %v", err)
	}
	if err := s.Invalidate(c); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("double invalidate: err = %v, want ErrInvalidSession", err)
	}
}

func TestCrossStoreCursorRejected(t *testing.T) {
	s1, _ := seed(t, 5)
	s2, _ := seed(t, 5)
	c := someCursor(t, s1)
	if _, err := s2.Scan(c, 2); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cross-store cursor: err = %v, want ErrInvalidCursor", err)
	}
}

func TestCursorDoesNotExposePlaintextKey(t *testing.T) {
	s, _ := seed(t, 5)
	c := someCursor(t, s)
	for _, k := range []string{"k000", "k001", "k002"} {
		if strings.Contains(c, k) {
			t.Fatalf("cursor %q leaks key %q", c, k)
		}
	}
}

func TestConcurrentCursorsIndependent(t *testing.T) {
	s, _ := seed(t, 6)
	p1, _ := s.Scan("", 2)
	p2, _ := s.Scan("", 4)
	a, _ := s.Scan(p1.Next, 2)
	b, _ := s.Scan(p2.Next, 2)
	if got := pageKeys(a); !equalKeys(got, []string{"k002", "k003"}) {
		t.Fatalf("traversal A page = %v", got)
	}
	if got := pageKeys(b); !equalKeys(got, []string{"k004", "k005"}) {
		t.Fatalf("traversal B page = %v", got)
	}
}
