package partscan

import (
	"bytes"
	"testing"
)

// Semantics 7: returned parts are independent of both internal buffers and
// the caller's input slice.
func TestCopyIsolation(t *testing.T) {
	boundary := "b"
	stream := []byte("--b\r\nhello\r\n--b\r\nworld\r\n--b--")

	// Feed via a mutable caller buffer, one byte at a time from one backing
	// array that we overwrite after the call.
	s := New(boundary)
	var got [][]byte
	tmp := make([]byte, 1)
	for _, c := range stream {
		tmp[0] = c
		ps, err := s.Feed(tmp)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ps...)
		tmp[0] = 0xFF // mutate caller slice after return
	}
	assertParts(t, got, [][]byte{[]byte("hello"), []byte("world")})

	// Definitive check: feed the first part plus a prefix of its delimiter,
	// mutate the input slice, then finish; output must be the original data.
	s3 := New(boundary)
	mutable := []byte("--b\r\nsecret-part\r\n--b\r")
	if _, err := s3.Feed(mutable); err != nil {
		t.Fatal(err)
	}
	for i := range mutable {
		mutable[i] = 'X'
	}
	rest := []byte("\n")
	restParts, err := s3.Feed(rest)
	if err != nil {
		t.Fatal(err)
	}
	// The part must not have been emitted yet (delimiter split); finish.
	finParts, err := s3.Feed([]byte("x\r\n--b--"))
	if err != nil {
		t.Fatal(err)
	}
	emitted := append(append([][]byte{}, restParts...), finParts...)
	assertParts(t, emitted, [][]byte{[]byte("secret-part"), []byte("x")})
}

// Semantics 8: pending storage must not grow with total stream length.
func TestBoundedPending(t *testing.T) {
	s := New("b")
	if _, err := s.Feed([]byte("--b\r\n")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10000; i++ {
		last := i == 9999
		var chunk []byte
		if last {
			chunk = []byte("pp\r\n--b--")
		} else {
			chunk = []byte("pp\r\n--b\r\n")
		}
		ps, err := s.Feed(chunk)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if len(ps) != 1 || string(ps[0]) != "pp" {
			t.Fatalf("iter %d: %q", i, ps)
		}
		if !last && s.Pending() != 0 {
			t.Fatalf("iter %d pending=%d, want 0", i, s.Pending())
		}
	}
	if !s.Done() || s.Close() != nil {
		t.Fatalf("done=%v close=%v", s.Done(), s.Close())
	}
	if s.Pending() != 0 {
		t.Fatalf("final pending=%d, want 0", s.Pending())
	}

	// A single large part keeps pending proportional to that part only.
	big := bytes.Repeat([]byte("z"), 50000)
	s3 := New("b")
	if _, err := s3.Feed([]byte("--b\r\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := s3.Feed(big); err != nil {
		t.Fatal(err)
	}
	if s3.Pending() != len(big) {
		t.Fatalf("large-part pending=%d", s3.Pending())
	}
}
