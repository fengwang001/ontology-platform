package ontology

import (
	"reflect"
	"testing"
)

func TestLineAlignmentSkeleton(t *testing.T) {
	cases := []struct {
		name   string
		child  []string
		parent []string
		want   []int
	}{
		{"earlier child wins", []string{"x", "a", "a"}, []string{"a"}, []int{-1, 0, -1}},
		{"earlier parent wins", []string{"a"}, []string{"a", "a"}, []int{0}},
		{"maximum pairs", []string{"a", "b", "a"}, []string{"b", "a", "x"}, []int{-1, 0, 1}},
		{"byte exact only", []string{"A", "a\r\n", "a\n"}, []string{"a", "a\n"}, []int{-1, -1, 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchLines(tc.child, tc.parent)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("input=%#v,%#v actual=%v criterion=%v", tc.child, tc.parent, got, tc.want)
			}
			t.Logf("input=%#v,%#v actual=%v criterion=maximum ordered LCS, earliest tie", tc.child, tc.parent, got)
		})
	}
}

func TestSplitLinesPreservesTerminators(t *testing.T) {
	got := splitLines("a\n\nb")
	want := []string{"a\n", "\n", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("input=%q actual=%#v criterion=%#v", "a\n\nb", got, want)
	}
	t.Logf("input=%q actual=%#v criterion=byte-exact lines and newline suffixes", "a\n\nb", got)
}
