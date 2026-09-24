package resume

import (
	"errors"
	"reflect"
	"testing"

	"ontology/graph"
	"ontology/walk"
)

func sampleGraph() *graph.Graph {
	g := graph.New()
	for _, e := range [][2]string{
		{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}, {"c", "e"},
	} {
		g.AddEdge(e[0], e[1])
	}
	return g
}

func sampleCheckpoint(t *testing.T, g *graph.Graph) walk.Checkpoint {
	t.Helper()
	res, err := walk.Walk(g, walk.Initial("a"), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Next.Pending) == 0 {
		t.Fatal("test checkpoint should carry pending cursors")
	}
	return res.Next
}

func TestRoundTrip(t *testing.T) {
	g := sampleGraph()
	cases := []walk.Checkpoint{
		walk.Initial("a"),
		sampleCheckpoint(t, g),
		{}, // done checkpoint
	}
	for _, c := range cases {
		got, err := Decode(Encode(c), g)
		if err != nil {
			t.Fatalf("decode %+v: %v", c, err)
		}
		if !reflect.DeepEqual(got, c) {
			t.Fatalf("round trip: got %+v, want %+v", got, c)
		}
	}
}

func TestBitFlipAlwaysRejected(t *testing.T) {
	g := sampleGraph()
	data := Encode(sampleCheckpoint(t, g))
	counts := map[error]int{ErrIncomplete: 0, ErrUnknownNode: 0, ErrChecksum: 0}
	accepted := 0
	for i := range data {
		for bit := 0; bit < 8; bit++ {
			variant := append([]byte(nil), data...)
			variant[i] ^= 1 << bit
			_, err := Decode(variant, g)
			if err == nil {
				accepted++
				continue
			}
			matched := false
			for class := range counts {
				if errors.Is(err, class) {
					counts[class]++
					matched = true
				}
			}
			if !matched {
				t.Fatalf("byte %d bit %d: unclassified error %v", i, bit, err)
			}
		}
	}
	if accepted != 0 {
		t.Fatalf("%d tampered variants were accepted", accepted)
	}
	t.Logf("variants=%d incomplete=%d unknown-node=%d checksum=%d accepted=0",
		len(data)*8, counts[ErrIncomplete], counts[ErrUnknownNode], counts[ErrChecksum])
}

func TestTruncationIsIncomplete(t *testing.T) {
	g := sampleGraph()
	data := Encode(sampleCheckpoint(t, g))
	for _, n := range []int{0, 1, 2, len(data) / 2, len(data) - 1} {
		if _, err := Decode(data[:n], g); !errors.Is(err, ErrIncomplete) {
			t.Fatalf("truncate to %d: got %v, want ErrIncomplete", n, err)
		}
	}
}

func TestUnknownNodeClass(t *testing.T) {
	g := sampleGraph()
	bogus := walk.Checkpoint{Ready: []string{"zz"}}
	_, err := Decode(Encode(bogus), g)
	if !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("got %v, want ErrUnknownNode", err)
	}
	if errors.Is(err, ErrChecksum) || errors.Is(err, ErrIncomplete) {
		t.Fatalf("error classes overlap: %v", err)
	}
}
