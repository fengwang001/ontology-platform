package stack

import (
	"slices"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		name      string
		in        []string
		maxDepth  int
		want      []string
		truncated bool
		valid     bool
	}{
		{"empty", nil, 3, nil, false, false},
		{"single", []string{"A"}, 3, []string{"A"}, false, true},
		{"depth1", []string{"A"}, 1, []string{"A"}, false, true},
		{"adjacent dedup", []string{"A", "A", "B"}, 3, []string{"A", "B"}, false, true},
		{"nonadjacent kept", []string{"F", "G", "F"}, 3, []string{"F", "G", "F"}, false, true},
		{"empty frame name", []string{"", "B"}, 3, []string{"", "B"}, false, true},
		{"exact limit", []string{"A", "B", "C"}, 3, []string{"A", "B", "C"}, false, true},
		{"over limit", []string{"A", "B", "C", "D"}, 3, []string{"A", "B", "C"}, true, true},
		{"unlimited", []string{"A", "B", "C", "D"}, 0, []string{"A", "B", "C", "D"}, false, true},
		{"dedup then truncate", []string{"A", "A", "B", "C", "D"}, 2, []string{"A", "B"}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, tr := Normalize(tc.in, tc.maxDepth)
			if !slices.Equal(got, tc.want) || tr != tc.truncated || Valid(got) != tc.valid {
				t.Fatalf("Normalize(%v,%d) = %v,%v; want %v,%v valid=%v",
					tc.in, tc.maxDepth, got, tr, tc.want, tc.truncated, tc.valid)
			}
		})
	}
}
