package resume

import (
	"errors"
	"slices"
	"testing"

	"ontology/graph"
)

// Node IDs are pairwise at bit-Hamming distance >= 2, so any single-bit
// flip inside an ID byte yields a string that is not a node of the graph.
func testGraph() *graph.Graph {
	g := graph.New()
	g.AddEdge("aa", "bb")
	g.AddEdge("aa", "cc")
	g.AddEdge("bb", "dd")
	g.AddEdge("cc", "dd")
	g.AddEdge("dd", "ee")
	return g
}

func midState() State {
	return State{
		FrontCursor: 1,
		Queue:       []string{"aa", "bb", "cc"},
		Seen:        []string{"aa", "bb", "cc"},
	}
}

func TestRoundTrip(t *testing.T) {
	g := testGraph()
	cases := []struct {
		name string
		st   State
	}{
		{"initial", State{Queue: []string{"aa"}}},
		{"mid traversal", midState()},
		{"done", State{Done: true, Seen: []string{"aa", "bb", "cc", "dd", "ee"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decode(g, Encode(tc.st))
			if err != nil {
				t.Fatalf("Decode(Encode()): %v", err)
			}
			if got.Done != tc.st.Done || got.FrontCursor != tc.st.FrontCursor ||
				!slices.Equal(got.Queue, tc.st.Queue) || !slices.Equal(got.Seen, tc.st.Seen) {
				t.Fatalf("round trip = %+v, want %+v", got, tc.st)
			}
		})
	}
}

func TestEncodeDeterministic(t *testing.T) {
	st := midState()
	st.Seen = []string{"cc", "aa", "bb"} // unsorted input must still encode the same
	a, b := Encode(st), Encode(midState())
	if !slices.Equal(a, b) {
		t.Fatal("encoding must not depend on caller's seen ordering")
	}
}

func TestTruncation(t *testing.T) {
	g := testGraph()
	tok := Encode(midState())
	for n := 0; n < len(tok); n++ {
		if _, err := Decode(g, tok[:n]); !errors.Is(err, ErrIncomplete) {
			t.Fatalf("prefix %d: err=%v, want ErrIncomplete", n, err)
		}
	}
}

func TestBitFlipClassification(t *testing.T) {
	g := testGraph()
	tok := Encode(midState())
	counts := map[error]int{}
	for i := 0; i < len(tok); i++ {
		for bit := 0; bit < 8; bit++ {
			bad := make([]byte, len(tok))
			copy(bad, tok)
			bad[i] ^= 1 << bit
			_, err := Decode(g, bad)
			if err == nil {
				t.Fatalf("byte %d bit %d: corrupted token accepted", i, bit)
			}
			switch {
			case errors.Is(err, ErrChecksum):
				counts[ErrChecksum]++
			case errors.Is(err, ErrIncomplete):
				counts[ErrIncomplete]++
			case errors.Is(err, ErrUnknownNode):
				counts[ErrUnknownNode]++
			default:
				t.Fatalf("byte %d bit %d: unclassified error %v", i, bit, err)
			}
		}
	}
	t.Logf("bits=%d checksum=%d incomplete=%d unknown=%d misaccepted=0",
		len(tok)*8, counts[ErrChecksum], counts[ErrIncomplete], counts[ErrUnknownNode])
	if counts[ErrChecksum] == 0 || counts[ErrIncomplete] == 0 || counts[ErrUnknownNode] == 0 {
		t.Fatalf("each category must be exercised: %v", counts)
	}
}

func TestUnknownNode(t *testing.T) {
	g := testGraph()
	st := midState()
	st.Queue = []string{"aa", "zz", "cc"} // well-formed token, node not in graph
	st.Seen = []string{"aa", "cc", "zz"}
	_, err := Decode(g, Encode(st))
	if !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("err=%v, want ErrUnknownNode", err)
	}
	if errors.Is(err, ErrChecksum) || errors.Is(err, ErrIncomplete) {
		t.Fatalf("categories must be distinguishable: %v", err)
	}
}

func TestUnsortedSeenRejected(t *testing.T) {
	g := testGraph()
	tok := Encode(State{Queue: []string{"aa"}, Seen: []string{"aa", "cc"}})
	// Seen entries have equal length; swap the two 3-byte entry blocks
	// (header 19 + one queue entry 3 = offset 22) to make seen unsorted.
	bad := slices.Clone(tok)
	for i := 0; i < 3; i++ {
		bad[22+i], bad[25+i] = bad[25+i], bad[22+i]
	}
	if _, err := Decode(g, bad); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("err=%v, want ErrIncomplete", err)
	}
}

func TestInconsistentShape(t *testing.T) {
	g := testGraph()
	cases := []struct {
		name string
		st   State
	}{
		{"empty queue not done", State{Seen: []string{"aa"}}},
		{"done with queue", State{Done: true, Queue: []string{"aa"}, Seen: []string{"aa"}}},
		{"queued node not seen", State{Queue: []string{"aa", "bb"}, Seen: []string{"aa"}}},
		{"cursor past out-degree", State{Queue: []string{"ee"}, FrontCursor: 1, Seen: []string{"ee"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode(g, Encode(tc.st)); !errors.Is(err, ErrIncomplete) {
				t.Fatalf("err=%v, want ErrIncomplete", err)
			}
		})
	}
}
