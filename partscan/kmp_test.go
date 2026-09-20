package partscan

import "testing"

func TestKMPBasic(t *testing.T) {
	m := newKMP([]byte("aba"))
	seq := []byte("xabyababa")
	var hits []int
	for i, b := range seq {
		if m.push(b) {
			hits = append(hits, i)
		}
	}
	// "aba" ends at indices 4 ("yab a")? stream: x a b y a b a b a
	// matches end at 6 and 8.
	if len(hits) != 2 || hits[0] != 6 || hits[1] != 8 {
		t.Fatalf("hits=%v", hits)
	}
}

func TestKMPFailStateOverlap(t *testing.T) {
	// Pattern ending such that a disproved match can overlap itself.
	m := newKMP([]byte("\r\n--b"))
	full := []byte("\r\n--b")
	for _, b := range full {
		m.push(b)
	}
	if m.state != len(full) {
		t.Fatalf("state=%d", m.state)
	}
	// Look-ahead byte that fails the marker but starts "\r" anew.
	if m.failState('\r') {
		t.Fatal("one byte should not complete the pattern again")
	}
	rest := []byte("\n--b")
	var matched bool
	for _, b := range rest {
		matched = m.push(b)
	}
	if !matched {
		t.Fatal("overlapping rematch failed")
	}
}
