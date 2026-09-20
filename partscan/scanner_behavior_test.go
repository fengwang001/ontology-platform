package partscan

import (
	"errors"
	"testing"
)

// Semantics 3: adjacent delimiters yield empty (non-nil) parts.
func TestEmptyParts(t *testing.T) {
	stream := []byte("--b\r\n\r\n--b\r\n\r\n--b\r\n\r\n--b--")
	for _, sizes := range [][]int{{len(stream)}, {1}, {2}, {5}} {
		got, err := feedBySizes(New("b"), stream, sizes...)
		if err != nil {
			t.Fatalf("sizes %v: %v", sizes, err)
		}
		if len(got) != 3 {
			t.Fatalf("sizes %v: got %d parts", sizes, len(got))
		}
		for i, p := range got {
			if p == nil {
				t.Fatalf("part %d is nil; want non-nil empty slice", i)
			}
			if len(p) != 0 {
				t.Fatalf("part %d = %q, want empty", i, p)
			}
		}
	}
}

// Semantics 4: a stream not starting with the preamble is a terminal error.
func TestNoPreamble(t *testing.T) {
	for _, bad := range [][]byte{
		[]byte("nope"),
		[]byte("--wrong\r\nx"),
		[]byte("--b\rX"), // wrong fourth char
	} {
		s := New("b")
		if _, err := s.Feed(bad); !errors.Is(err, ErrNoPreamble) {
			t.Fatalf("input %q: got %v, want ErrNoPreamble", bad, err)
		}

		// Every later Feed must return the same terminal error.
		if _, err := s.Feed([]byte("--b\r\nx")); !errors.Is(err, ErrNoPreamble) {
			t.Fatalf("post-error Feed for %q: %v", bad, err)
		}
		if _, err := s.Feed(nil); !errors.Is(err, ErrNoPreamble) {
			t.Fatalf("post-error empty Feed for %q: %v", bad, err)
		}
		if s.Done() {
			t.Fatalf("input %q: Done should be false", bad)
		}
	}

	// A still-complete prefix that never finishes is incomplete, not invalid.
	sTrunc := New("b")
	if _, err := sTrunc.Feed([]byte("--b")); err != nil {
		t.Fatalf("truncated prefix Feed: %v", err)
	}
	if err := sTrunc.Close(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("truncated prefix Close: %v", err)
	}
}

// Semantics 5: closer handling, trailing bytes ignored, late Feed errors.
func TestAfterClose(t *testing.T) {
	stream := []byte("--b\r\nonly\r\n--b--")

	// Closer split byte-by-byte, trailing garbage arrives afterwards.
	s := New("b")
	var got [][]byte
	for i := 0; i < len(stream); i++ {
		ps, err := s.Feed(stream[i : i+1])
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ps...)
	}
	if !s.Done() {
		t.Fatal("Done should be true after closer")
	}
	assertParts(t, got, [][]byte{[]byte("only")})

	// Garbage already buffered at the moment the closer completes is ignored.
	if _, err := s.Feed([]byte("ignored garbage")); !errors.Is(err, ErrAfterClose) {
		t.Fatalf("late Feed: %v", err)
	}
	// Empty Feed after close is fine; error is sticky afterwards.
	if _, err := s.Feed(nil); !errors.Is(err, ErrAfterClose) {
		t.Fatalf("empty Feed after error: %v", err)
	}
	if err := s.Close(); !errors.Is(err, ErrAfterClose) {
		t.Fatalf("Close after late feed: %v", err)
	}

	// Trailing bytes arriving in the same chunk as the closer are ignored
	// without an error from that Feed.
	s2 := New("b")
	got2, err := s2.Feed(append(append([]byte{}, stream...), []byte("trailing123")...))
	if err != nil {
		t.Fatalf("same-chunk trailing bytes: %v", err)
	}
	if !s2.Done() {
		t.Fatal("Done with same-chunk trailing")
	}
	assertParts(t, got2, [][]byte{[]byte("only")})
	if err := s2.Close(); err != nil {
		t.Fatalf("Close after done: %v", err)
	}
}

// Semantics 6: Close before the closer is ErrIncomplete; repeatable.
func TestCloseIncomplete(t *testing.T) {
	s := New("b")
	if _, err := s.Feed([]byte("--b\r\nabc")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := s.Close(); !errors.Is(err, ErrIncomplete) {
			t.Fatalf("Close #%d: %v", i, err)
		}
	}

	// Mid-delimiter at end is also incomplete.
	s2 := New("b")
	if _, err := s2.Feed([]byte("--b\r\nx\r\n--b\r")); err != nil {
		t.Fatal(err)
	}
	if err := s2.Close(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("partial-delimiter Close: %v", err)
	}

	// Completed stream closes nil, repeatably.
	done, _ := feedBySizes(New("b"), []byte("--b\r\n\r\n--b--"), 1)
	_ = done
	s3 := New("b")
	if _, err := s3.Feed([]byte("--b\r\n\r\n--b--")); err != nil {
		t.Fatal(err)
	}
	if err := s3.Close(); err != nil {
		t.Fatalf("completed Close: %v", err)
	}
	if err := s3.Close(); err != nil {
		t.Fatalf("repeated completed Close: %v", err)
	}
}
