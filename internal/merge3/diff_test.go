package merge3

import (
	"slices"
	"testing"
)

// applyDiff rebuilds side from base and a (keep, ins) alignment.
func applyDiff(base []string, keep []bool, ins [][]string) []string {
	var out []string
	for i := 0; i <= len(base); i++ {
		out = append(out, ins[i]...)
		if i < len(base) && keep[i] {
			out = append(out, base[i])
		}
	}
	return out
}

func TestDiffReconstructsSide(t *testing.T) {
	cases := []struct {
		base, side []string
	}{
		{nil, nil},
		{nil, []string{"x"}},
		{[]string{"a"}, nil},
		{[]string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{[]string{"a", "b", "c"}, []string{"a", "X", "c"}},
		{[]string{"a", "b", "c"}, []string{"a", "c"}},
		{[]string{"a", "c"}, []string{"a", "b", "c"}},
		{[]string{"a", "b", "c"}, []string{"x", "a", "b", "c", "y"}},
		{[]string{"a", "a", "b"}, []string{"a", "b", "b"}},
		{[]string{"1", "2", "3", "4"}, []string{"4", "3", "2", "1"}},
	}
	for _, tc := range cases {
		keep, ins := diffLines(tc.base, tc.side)
		if len(keep) != len(tc.base) || len(ins) != len(tc.base)+1 {
			t.Fatalf("diffLines(%q, %q): bad shape", tc.base, tc.side)
		}
		if got := applyDiff(tc.base, keep, ins); !slices.Equal(got, tc.side) {
			t.Errorf("diffLines(%q, %q): reconstruct = %q", tc.base, tc.side, got)
		}
	}
}

func TestDiffAnchors(t *testing.T) {
	keep, ins := diffLines([]string{"a", "b", "c"}, []string{"a", "x", "b", "c"})
	if !slices.Equal(keep, []bool{true, true, true}) {
		t.Fatalf("keep = %v", keep)
	}
	if !slices.Equal(ins[1], []string{"x"}) {
		t.Fatalf("ins[1] = %q, want [x]", ins[1])
	}
	for i, got := range ins {
		if i != 1 && len(got) != 0 {
			t.Fatalf("ins[%d] = %q, want empty", i, got)
		}
	}

	keep, ins = diffLines([]string{"a", "b", "c"}, []string{"a", "c"})
	if !slices.Equal(keep, []bool{true, false, true}) {
		t.Fatalf("keep = %v", keep)
	}
	for i, got := range ins {
		if len(got) != 0 {
			t.Fatalf("ins[%d] = %q, want empty", i, got)
		}
	}
}
