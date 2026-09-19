package ontology

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)
func TestTamperedAndTruncatedCursor(t *testing.T) {
	s := seedStore(4)
	p, _ := s.Scan("", 2)

	// Garbage strings are structurally invalid.
	for _, bad := range []string{"not-a-cursor", "!!!", "", "AAAA"} {
		if bad == "" {
			continue // empty cursor legitimately starts a traversal
		}
		if _, err := s.Scan(bad, 2); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("cursor %q: want ErrInvalidCursor, got %v", bad, err)
		}
	}

	// Truncated token: remove trailing characters.
	if _, err := s.Scan(p.Next[:len(p.Next)-4], 2); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("truncated cursor: want ErrInvalidCursor, got %v", err)
	}

	// Flip a byte of the body (keeps length), invalidating the MAC.
	raw, err := base64.RawURLEncoding.DecodeString(p.Next)
	if err != nil {
		t.Fatal(err)
	}
	flipped := make([]byte, len(raw))
	copy(flipped, raw)
	flipped[2] ^= 0xFF
	tok := base64.RawURLEncoding.EncodeToString(flipped)
	if _, err := s.Scan(tok, 2); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("tampered cursor: want ErrInvalidCursor, got %v", err)
	}

	// Flip only the MAC tail instead.
	copy(flipped, raw)
	flipped[len(flipped)-1] ^= 0x01
	if _, err := s.Scan(base64.RawURLEncoding.EncodeToString(flipped), 2); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("tampered MAC: want ErrInvalidCursor, got %v", err)
	}
}

func TestCursorDoesNotLeakKeys(t *testing.T) {
	s := seedStore(3)
	p, _ := s.Scan("", 2)
	for _, secret := range []string{"k00", "k01", "k02"} {
		if strings.Contains(p.Next, secret) {
			t.Fatalf("cursor %q leaks key %q", p.Next, secret)
		}
	}
	raw, err := base64.RawURLEncoding.DecodeString(p.Next)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []byte("k01") {
		_ = secret
	}
	if strings.Contains(string(raw), "k0") {
		t.Fatalf("decoded cursor body leaks keys: %x", raw)
	}
}

func TestCrossSessionInvalidationIsDistinctError(t *testing.T) {
	s := seedStore(6)

	sess := s.BeginTraversal()
	p, err := s.Scan("", 2) // a separate, fresh session
	if err != nil {
		t.Fatal(err)
	}
	_ = sess

	// A well-formed cursor from an explicitly invalidated session must fail
	// with ErrSessionInvalid, not ErrInvalidCursor.
	victim := s.BeginTraversal()
	vp, verr := s.Scan("", 2)
	_ = vp
	victim.Invalidate()
	// Re-mint a cursor for the victim session via a scan before invalidation:
	// capture via second session cursor tied to victim is not exposed, so we
	// use the page produced while alive and invalidate afterward.
	_ = verr

	// Restart cleanly: page then invalidate.
	victim2 := s.BeginTraversal()
	_ = victim2
	c, _ := s.Scan("", 2)
	// Invalidate the session that owns c by tracking it through decode.
	owner, idx, derr := s.decodeCursor(c.Next)
	if derr != nil {
		t.Fatal(derr)
	}
	if idx != 2 {
		t.Fatalf("index = %d, want 2", idx)
	}
	owner.Invalidate()
	_, err = s.Scan(c.Next, 2)
	if !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("want ErrSessionInvalid, got %v", err)
	}
	if errors.Is(err, ErrInvalidCursor) {
		t.Fatal("ErrSessionInvalid must not also match ErrInvalidCursor")
	}

	// The unrelated live session cursor still works.
	if _, err := s.Scan(p.Next, 2); err != nil {
		t.Fatalf("live session cursor broke: %v", err)
	}
}
