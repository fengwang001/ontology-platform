package partscan

import (
	"bytes"
	"math/rand"
	"testing"
)

// Semantics 1: output is identical regardless of how the stream is chunked.
func TestSplitInvariance(t *testing.T) {
	boundary := "theBoundary42"
	parts := [][]byte{
		[]byte("plain short part"),
		{},
		bytes.Repeat([]byte("x"), 1000),
		[]byte("line1\r\nline2\r\nline3"),
		// Prefixes of the delimiter that ultimately do not match.
		[]byte("a\r\n--theBoundaryxx"),
		[]byte("\r\n-"),
		[]byte("\r\n--theBoundary"),
		[]byte("z\r\n--theBoundary\r"), // missing \n
		[]byte("\r\n--theBoundary-x"),
		[]byte("trailing"),
	}
	stream := buildStream(boundary, parts)

	// Reference: all at once.
	ref, err := New(boundary).Feed(stream)
	if err != nil {
		t.Fatalf("bulk feed: %v", err)
	}
	if err := New(boundary).Close(); err == nil {
		t.Fatal("fresh scanner Close should be ErrIncomplete")
	}

	chunkings := [][]int{
		{len(stream)},
		{1},
		{2},
		{3},
		{7},
		{64},
	}
	// Deliberately split right through every "\r\n--b" occurrence.
	for cut := 0; cut < len(stream); cut++ {
		chunkings = append(chunkings, []int{cut, len(stream) - cut})
	}

	for ci, sizes := range chunkings {
		s := New(boundary)
		got, err := feedBySizes(s, stream, sizes...)
		if err != nil {
			t.Fatalf("chunking %d: %v", ci, err)
		}
		if !s.Done() {
			t.Fatalf("chunking %d: not done", ci)
		}
		assertParts(t, got, ref)
	}

	// Deterministic random splits.
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 200; iter++ {
		s := New(boundary)
		var got [][]byte
		for off := 0; off < len(stream); {
			n := 1 + rng.Intn(20)
			end := off + n
			if end > len(stream) {
				end = len(stream)
			}
			ps, err := s.Feed(stream[off:end])
			if err != nil {
				t.Fatalf("random iter %d: %v", iter, err)
			}
			got = append(got, ps...)
			off = end
		}
		if !s.Done() {
			t.Fatalf("random iter %d: not done", iter)
		}
		assertParts(t, got, ref)
	}
}

// Semantics 2: delimiter prefixes that fail to complete survive verbatim.
func TestDelimiterPrefixesPreserved(t *testing.T) {
	boundary := "b"
	// "\r\n--bX" is not a delimiter (suffix after marker must be \r\n or --);
	// it must appear in full inside the part.
	inner := []byte("start\r\n--bXmiddle\r\n--b-\r\n--b-?\r\n--b\rq")
	parts := [][]byte{inner, []byte("ok")}
	stream := buildStream(boundary, parts)

	var got [][]byte
	s := New(boundary)
	for i := 0; i < len(stream); i++ {
		ps, err := s.Feed(stream[i : i+1])
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ps...)
	}
	assertParts(t, got, parts)
}
