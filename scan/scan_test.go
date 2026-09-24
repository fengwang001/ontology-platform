package scan

import (
	"strings"
	"testing"

	"ontology/table"
)

func TestScanLinear(t *testing.T) {
	cases := []struct{ n, m int }{{1000, 10}, {100000, 100}}
	for _, tc := range cases {
		text := strings.Repeat("a", tc.n)
		pat := strings.Repeat("a", tc.m-1) + "b"
		s := New(pat, table.Build(pat))
		s.Scan(text)
		if s.advances != tc.n {
			t.Errorf("n=%d m=%d: advances=%d, want exactly %d", tc.n, tc.m, s.advances, tc.n)
		}
		if s.compares > 2*tc.n {
			t.Errorf("n=%d m=%d: compares=%d, want <= %d", tc.n, tc.m, s.compares, 2*tc.n)
		}
	}
}
