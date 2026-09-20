package merge3

import (
	"reflect"
	"testing"
)

func TestDiffNoChange(t *testing.T) {
	if got := diff([]string{"a", "b"}, []string{"a", "b"}); got != nil {
		t.Fatalf("diff of identical input = %v, want nil", got)
	}
}

func TestDiffPureInsertion(t *testing.T) {
	got := diff([]string{"a", "b"}, []string{"a", "x", "y", "b"})
	want := []hunk{{start: 1, end: 1, lines: []string{"x", "y"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diff = %+v, want %+v", got, want)
	}
}

func TestDiffPureDeletion(t *testing.T) {
	got := diff([]string{"a", "x", "y", "b"}, []string{"a", "b"})
	want := []hunk{{start: 1, end: 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diff = %+v, want %+v", got, want)
	}
}

func TestDiffReplacement(t *testing.T) {
	got := diff([]string{"a", "b", "c"}, []string{"a", "B", "c"})
	want := []hunk{{start: 1, end: 2, lines: []string{"B"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diff = %+v, want %+v", got, want)
	}
}

func TestDiffEmptyBase(t *testing.T) {
	got := diff(nil, []string{"n1", "n2"})
	want := []hunk{{start: 0, end: 0, lines: []string{"n1", "n2"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diff = %+v, want %+v", got, want)
	}
}

func TestDiffEmptySide(t *testing.T) {
	got := diff([]string{"n1", "n2"}, nil)
	want := []hunk{{start: 0, end: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diff = %+v, want %+v", got, want)
	}
}

// Applying the hunks to base must reproduce side exactly.
func TestDiffRoundTrip(t *testing.T) {
	cases := []struct{ base, side []string }{
		{[]string{"a", "b", "c"}, []string{"b", "c", "d"}},
		{[]string{"a", "b", "a", "b"}, []string{"b", "a", "b"}},
		{[]string{"x"}, []string{"a", "b", "c"}},
		{[]string{"1", "2", "3", "4", "5"}, []string{"1", "3", "5"}},
		{nil, nil},
	}
	for _, tc := range cases {
		var out []string
		pos := 0
		for _, h := range diff(tc.base, tc.side) {
			out = append(out, tc.base[pos:h.start]...)
			out = append(out, h.lines...)
			pos = h.end
		}
		out = append(out, tc.base[pos:]...)
		if !reflect.DeepEqual(out, tc.side) && !(len(out) == 0 && len(tc.side) == 0) {
			t.Fatalf("apply(diff(%q, %q)) = %q", tc.base, tc.side, out)
		}
	}
}
