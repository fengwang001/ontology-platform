package canon_test

import (
	"strings"
	"testing"

	"ontology/canon"
)

func smallLimits() canon.Limits {
	return canon.Limits{MaxURLLength: 64, MaxPathSegments: 4, MaxQueryParams: 3}
}

func TestThreeLimitsDistinguishable(t *testing.T) {
	n := canon.New(canon.ModeOrdered, smallLimits())
	cases := []struct {
		url  string
		kind canon.LimitKind
	}{
		{"http://h/" + strings.Repeat("a", 100), canon.LimitLength},
		{"http://h/a/b/c/d/e", canon.LimitSegments},
		{"http://h/p?a=1&b=2&c=3&d=4", canon.LimitParams},
	}
	seen := map[canon.LimitKind]bool{}
	for _, c := range cases {
		r, err := n.Normalize(c.url)
		if err == nil {
			t.Fatalf("Normalize(%q) succeeded: %q", c.url, r.Canonical)
		}
		le, ok := err.(*canon.LimitError)
		if !ok {
			t.Fatalf("error type %T, want *canon.LimitError", err)
		}
		if le.Kind != c.kind {
			t.Errorf("kind = %v, want %v", le.Kind, c.kind)
		}
		seen[le.Kind] = true
	}
	if len(seen) != 3 {
		t.Fatalf("limit errors not distinguishable: %v", seen)
	}
}

// TestRejectionChangesNoState: after over-limit rejections, previously
// obtained results must still be reproducible byte-for-byte.
func TestRejectionChangesNoState(t *testing.T) {
	n := canon.New(canon.ModeSorted, smallLimits())
	good := "http://h/a?x=1"
	before, err := n.Normalize(good)
	if err != nil {
		t.Fatal(err)
	}
	rejects := []string{
		"http://h/" + strings.Repeat("a", 100),
		"http://h/a/b/c/d/e",
		"http://h/p?a=1&b=2&c=3&d=4",
	}
	for _, u := range rejects {
		if _, err := n.Normalize(u); err == nil {
			t.Fatalf("expected rejection of %q", u)
		}
	}
	after, err := n.Normalize(good)
	if err != nil {
		t.Fatal(err)
	}
	if before.Canonical != after.Canonical {
		t.Errorf("state changed by rejections: %q vs %q", before.Canonical, after.Canonical)
	}
}

// TestLengthRejectedBeforeScan: a too-long URL is rejected before any
// byte is scanned, so the scan counter does not move.
func TestLengthRejectedBeforeScan(t *testing.T) {
	n := canon.New(canon.ModeOrdered, smallLimits())
	base := n.ScanCount()
	if _, err := n.Normalize("http://h/" + strings.Repeat("z", 100)); err == nil {
		t.Fatal("expected length rejection")
	}
	if n.ScanCount() != base {
		t.Errorf("scan counter advanced on length rejection: %d -> %d", base, n.ScanCount())
	}
}
