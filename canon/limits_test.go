package canon

import (
	"errors"
	"strings"
	"testing"
)

func limited() *Normalizer {
	return New(Config{MaxLength: 64, MaxSegments: 4, MaxParams: 3})
}

func TestLimitErrorsDistinguishable(t *testing.T) {
	n := limited()
	longURL := "http://h/" + strings.Repeat("a", 100)
	deepURL := "http://h/a/b/c/d/e"
	wideURL := "http://h/?a=1&b=2&c=3&d=4"
	if _, err := n.Normalize(longURL); !errors.Is(err, ErrTooLong) {
		t.Fatalf("length: %v", err)
	}
	if _, err := n.Normalize(deepURL); !errors.Is(err, ErrTooManySegments) {
		t.Fatalf("segments: %v", err)
	}
	if _, err := n.Normalize(wideURL); !errors.Is(err, ErrTooManyParams) {
		t.Fatalf("params: %v", err)
	}
	if errors.Is(ErrTooLong, ErrTooManySegments) ||
		errors.Is(ErrTooManySegments, ErrTooManyParams) ||
		errors.Is(ErrTooLong, ErrTooManyParams) {
		t.Fatal("limit errors must be mutually distinguishable")
	}
}

func TestRejectionChangesNoState(t *testing.T) {
	n := limited()
	before, err := n.Normalize("http://h/a?x=1")
	if err != nil {
		t.Fatal(err)
	}
	scans := n.ScanCount()
	rejects := []string{
		"http://h/" + strings.Repeat("a", 100),
		"http://h/a/b/c/d/e",
		"http://h/?a=1&b=2&c=3&d=4",
	}
	for _, rj := range rejects {
		if _, err := n.Normalize(rj); err == nil {
			t.Fatalf("%q: expected rejection", rj)
		}
	}
	if got := n.ScanCount(); got != scans {
		t.Fatalf("scan counter moved on rejection: %d -> %d", scans, got)
	}
	after, err := n.Normalize("http://h/a?x=1")
	if err != nil || after != before {
		t.Fatalf("state leaked across rejections: %q vs %q", before, after)
	}
}

func TestInspectIsReadOnly(t *testing.T) {
	n := New(Config{})
	r1, err := n.Inspect("HTTP://Example.com.:80/a/./b?b=2&a=1#f")
	if err != nil {
		t.Fatal(err)
	}
	scans := n.ScanCount()
	r2, err := n.Inspect("HTTP://Example.com.:80/a/./b?b=2&a=1#f")
	if err != nil {
		t.Fatal(err)
	}
	if n.ScanCount() != scans {
		t.Fatal("Inspect advanced the scan counter")
	}
	if r1.Canonical != r2.Canonical || r1.Kinds != r2.Kinds ||
		r1.Rewritten != r2.Rewritten || len(r1.Segments) != len(r2.Segments) ||
		len(r1.Query) != len(r2.Query) {
		t.Fatalf("unstable inspect: %+v vs %+v", r1, r2)
	}
	if r1.Canonical != "http://example.com/a/b?b=2&a=1" {
		t.Fatalf("canonical: %q", r1.Canonical)
	}
	if !r1.Rewritten || r1.Kinds == 0 {
		t.Fatalf("rewrites not reported: %+v", r1)
	}
	if r1.Scheme != "http" || r1.Host != "example.com" || r1.Port != "" {
		t.Fatalf("parts: %+v", r1)
	}
	if len(r1.Segments) != 2 || r1.Segments[0] != "a" || r1.Segments[1] != "b" {
		t.Fatalf("segments: %v", r1.Segments)
	}
	if len(r1.Query) != 2 || r1.Query[0].Key != "b" || r1.Query[1].Key != "a" {
		t.Fatalf("query: %v", r1.Query)
	}
}
