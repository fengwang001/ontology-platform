package rec

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func ev(off int64, val string) Event { return Event{Offset: off, Value: val} }

// kindOf maps an Apply outcome to NOTES marks: a/=/C/R/N.
func kindOf(applied bool, err error) byte {
	switch {
	case errors.Is(err, ErrNegativeOffset):
		return 'N'
	case errors.Is(err, ErrConflict):
		return 'C'
	case errors.Is(err, ErrRewound):
		return 'R'
	case applied:
		return 'a'
	default:
		return '='
	}
}

func dump(st *State) string {
	var b strings.Builder
	for _, p := range st.Snapshot() {
		fmt.Fprintf(&b, "%d:%s ", p.Offset, p.Value)
	}
	return b.String()
}

// TestTenStepWalkthrough pins the ten-row NOTES table row by row.
func TestTenStepWalkthrough(t *testing.T) {
	evs := []Event{ev(1, "a"), ev(2, "b"), ev(3, "c"), ev(2, "b"), ev(3, "x"),
		ev(4, "d"), ev(1, "z"), ev(5, "e"), ev(2, "b"), ev(1, "a")}
	wantMark := "aaa=CaCa=R"
	wantMax := []int64{1, 2, 3, 3, 3, 4, 4, 5, 5, 5}
	wantTab := []string{
		"1:a ", "1:a 2:b ", "1:a 2:b 3:c ", "1:a 2:b 3:c ", "1:a 2:b 3:c ",
		"1:a 2:b 3:c 4:d ", "1:a 2:b 3:c 4:d ", "2:b 3:c 4:d 5:e ",
		"2:b 3:c 4:d 5:e ", "2:b 3:c 4:d 5:e "}
	st := New(4)
	for i, e := range evs {
		applied, err := st.Apply(e)
		if kindOf(applied, err) != wantMark[i] || st.Max() != wantMax[i] || dump(st) != wantTab[i] {
			t.Fatalf("step %d: %c max=%d tab=%q want %c %d %q",
				i+1, kindOf(applied, err), st.Max(), dump(st), wantMark[i], wantMax[i], wantTab[i])
		}
	}
}

// TestInvariantIdempotent: repeated (offset,value) equals applying it once.
func TestInvariantIdempotent(t *testing.T) {
	cases := []struct {
		window int
		off    int64
		val    string
		pre    []Event
	}{
		{4, 3, "c", []Event{ev(1, "a"), ev(2, "b"), ev(3, "c")}},
		{1, 7, "z", []Event{ev(6, "p"), ev(7, "z")}},
		{8, 1, "x", []Event{ev(1, "x")}},
	}
	for _, c := range cases {
		st := New(c.window)
		for _, e := range c.pre {
			if _, err := st.Apply(e); err != nil {
				t.Fatal(err)
			}
		}
		want, wantMax := st.Snapshot(), st.Max()
		for k := 0; k < 5; k++ {
			applied, err := st.Apply(ev(c.off, c.val))
			if applied || err != nil || st.Max() != wantMax || !reflect.DeepEqual(st.Snapshot(), want) {
				t.Fatalf("repeat #%d changed state: applied=%v err=%v", k, applied, err)
			}
		}
	}
}

// TestInvariantMonotonicMax: max never moves on rejects or idempotents.
func TestInvariantMonotonicMax(t *testing.T) {
	st := New(4)
	steps := []Event{ev(2, "b"), ev(2, "b"), ev(2, "z"), ev(-1, "n"), ev(5, "e"),
		ev(1, "a"), ev(0, "q"), ev(6, "f"), ev(5, "e")}
	want := []byte{'a', '=', 'C', 'N', 'a', 'R', 'R', 'a', '='}
	var last int64
	for i, e := range steps {
		applied, err := st.Apply(e)
		if got := kindOf(applied, err); got != want[i] {
			t.Fatalf("step %d: got %c want %c", i+1, got, want[i])
		}
		if st.Max() < last || (err != nil && st.Max() != last) {
			t.Fatalf("step %d: max moved incorrectly (last=%d now=%d)", i+1, last, st.Max())
		}
		last = st.Max()
	}
}

// TestInvariantRejectLeavesNoTrace: branches ①④⑤ write nothing.
func TestInvariantRejectLeavesNoTrace(t *testing.T) {
	cases := []struct {
		name string
		pre  []Event
		bad  Event
		want error
	}{
		{"negative", []Event{ev(1, "a")}, ev(-3, "n"), ErrNegativeOffset},
		{"conflict", []Event{ev(1, "a"), ev(2, "b")}, ev(2, "X"), ErrConflict},
		{"rewind", []Event{ev(1, "a"), ev(2, "b"), ev(3, "c"), ev(4, "d"), ev(5, "e")},
			ev(1, "a"), ErrRewound},
	}
	for _, c := range cases {
		st := New(4)
		for _, e := range c.pre {
			if _, err := st.Apply(e); err != nil {
				t.Fatal(err)
			}
		}
		tab, mx := st.Snapshot(), st.Max()
		if _, err := st.Apply(c.bad); !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v want %v", c.name, err, c.want)
		}
		if st.Max() != mx || !reflect.DeepEqual(st.Snapshot(), tab) {
			t.Fatalf("%s: reject left a trace", c.name)
		}
	}
}

// TestEvictionChecksBounded proves eviction inspects only the window edge:
// checked is read directly (never via exported API) and must not grow with m.
func TestEvictionChecksBounded(t *testing.T) {
	const window = 8
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		st := New(window)
		for off := int64(1); off <= int64(m)+1; off++ { // last apply evicts exactly one entry
			if _, err := st.Apply(ev(off, "v")); err != nil {
				t.Fatal(err)
			}
		}
		if st.checked > 1+2 { // one truly evicted entry plus m-independent slack
			t.Fatalf("m=%d: inspected %d entries, expected O(1)", m, st.checked)
		}
	}
}
