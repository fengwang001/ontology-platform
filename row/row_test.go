package row

import (
	"reflect"
	"strconv"
	"testing"
)

type op struct {
	col string
	ts  int64
	val string
	del bool
}

func put(c string, ts int64, v string) op { return op{c, ts, v, false} }
func del(c string, ts int64) op           { return op{c, ts, "", true} }

func run(ops []op) *Row {
	r := New()
	for _, o := range ops {
		r.Apply(o.col, o.ts, o.val, o.del)
	}
	return r
}

// TestRowLookupConstant pins that locating a column inspects a constant
// number of entries regardless of row width (map, not a linear scan).
// White-box: same package, reads the unexported counter field directly.
func TestRowLookupConstant(t *testing.T) {
	const bound = 2
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		r := New()
		for i := 0; i < m; i++ {
			r.Apply("c"+strconv.Itoa(i), 1, "v", false)
		}
		r.Apply("c0", 2, "w", false) // hit existing column
		if r.lastChecked > bound {
			t.Fatalf("m=%d existing: checked %d entries", m, r.lastChecked)
		}
		r.Apply("newcol", 3, "w", false) // miss then insert
		if r.lastChecked > bound {
			t.Fatalf("m=%d insert: checked %d entries", m, r.lastChecked)
		}
		r.Apply("c0", 4, "", true) // tombstone path
		if r.lastChecked > bound {
			t.Fatalf("m=%d tombstone: checked %d entries", m, r.lastChecked)
		}
	}
}

func TestColumnIsolation(t *testing.T) {
	got := run([]op{put("c1", 5, "keep"), put("c2", 9, "x"), del("c2", 10),
		put("c3", 1, "y"), del("c3", 2)}).View()
	if !reflect.DeepEqual(got, map[string]string{"c1": "keep"}) {
		t.Fatalf("sibling columns not isolated: %v", got)
	}
}

func TestTombstoneSemantics(t *testing.T) {
	cases := [][]op{
		{del("c", 5), put("c", 4, "v")}, // tomb suppresses older value
		{del("c", 5), put("c", 5, "v")}, // tomb suppresses equal-ts value
		{del("c", 5), put("c", 6, "v")}, // newer value beats tomb
		{put("c", 6, "v"), del("c", 2)}, // stale tomb ignored
	}
	want := []map[string]string{{}, {}, {"c": "v"}, {"c": "v"}}
	for i := range cases {
		if got := run(cases[i]).View(); !reflect.DeepEqual(got, want[i]) {
			t.Fatalf("case %d: got %v want %v", i, got, want[i])
		}
	}
}
