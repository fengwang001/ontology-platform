package intervalindex

import (
	"fmt"
	"strings"
	"testing"
)

// op is one recorded call in a deterministic replay script.
type op struct {
	kind string
	id   int64
	lo   int64
	hi   int64
	x    int64
	b    int64
}

// replayScript is a fixed script exercising inserts, duplicates, removals,
// reinsertion with the same id, endpoint stabs, adjacent ranges and invalid
// arguments.
var replayScript = []op{
	{"insert", 3, 10, 20, 0, 0},
	{"insert", 1, 10, 20, 0, 0},
	{"insert", 2, 20, 30, 0, 0},
	{"insert", 3, 1, 2, 0, 0}, // duplicate id, rejected
	{"insert", 0, 1, 2, 0, 0}, // non-positive id, rejected
	{"insert", 4, 5, 5, 0, 0}, // zero width, rejected
	{"stab", 0, 0, 0, 10, 0},
	{"stab", 0, 0, 0, 20, 0},
	{"overlap", 0, 0, 0, 10, 20},
	{"overlap", 0, 0, 0, 20, 30},
	{"overlap", 0, 0, 0, 7, 7}, // zero width, rejected
	{"remove", 3, 0, 0, 0, 0},
	{"stab", 0, 0, 0, 15, 0},
	{"remove", 99, 0, 0, 0, 0}, // missing, rejected
	{"insert", 3, -5, 11, 0, 0},
	{"stab", 0, 0, 0, 10, 0},
	{"overlap", 0, 0, 0, -100, 100},
	{"len", 0, 0, 0, 0, 0},
}

func runScript(idx *Index) string {
	var sb strings.Builder
	for _, o := range replayScript {
		switch o.kind {
		case "insert":
			fmt.Fprintf(&sb, "insert id=%d [%d,%d) -> %v\n", o.id, o.lo, o.hi, idx.Insert(o.id, o.lo, o.hi))
		case "remove":
			fmt.Fprintf(&sb, "remove id=%d -> %v\n", o.id, idx.Remove(o.id))
		case "stab":
			fmt.Fprintf(&sb, "stab x=%d -> %v\n", o.x, idx.Stab(o.x))
		case "overlap":
			fmt.Fprintf(&sb, "overlap [%d,%d) -> ", o.x, o.b)
			got, err := idx.Overlap(o.x, o.b)
			fmt.Fprintf(&sb, "%v %v\n", got, err)
		case "len":
			fmt.Fprintf(&sb, "len -> %d\n", idx.Len())
		}
	}
	return sb.String()
}

// TestDeterministicReplay runs the same call sequence on two independent
// indices and requires byte-identical transcripts.
func TestDeterministicReplay(t *testing.T) {
	first := runScript(New())
	second := runScript(New())
	t.Logf("replay transcript:\n%s", first)
	if first != second {
		t.Fatalf("replays differ:\n%s----\n%s", first, second)
	}
}
