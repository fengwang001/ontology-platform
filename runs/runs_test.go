package runs

import (
	"strings"
	"testing"
)

func TestSplit(t *testing.T) {
	type pair struct {
		sym rune
		n   int
	}
	cases := []struct {
		in   string
		want []pair
	}{
		{"", nil},
		{"a", []pair{{'a', 1}}},
		{"aaab", []pair{{'a', 3}, {'b', 1}}},
		{"ééè", []pair{{'é', 2}, {'è', 1}}},
		{"e\u0301", []pair{{'e', 1}, {'\u0301', 1}}},
		{"aabbba", []pair{{'a', 2}, {'b', 3}, {'a', 1}}},
		{strings.Repeat("z", 70000), []pair{{'z', 70000}}},
	}
	for _, c := range cases {
		var got []pair
		for sym, n := range Split(c.in) {
			got = append(got, pair{sym, n})
		}
		if len(got) != len(c.want) {
			t.Fatalf("Split(%q) = %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("Split(%q) = %v, want %v", c.in, got, c.want)
			}
		}
	}
}

func TestWriteCount(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{2, "2"},
		{10, "10"},
		{70000, "70000"},
		{999999999, "999999999"},
	}
	for _, c := range cases {
		var b strings.Builder
		WriteCount(&b, c.n)
		if b.String() != c.want {
			t.Errorf("WriteCount(%d) = %q, want %q", c.n, b.String(), c.want)
		}
	}
}

func TestReadCount(t *testing.T) {
	cases := []struct {
		in   string
		off  int
		want string
		next int
	}{
		{"2a", 0, "2", 1},
		{"x12b", 1, "12", 3},
		{"99999999999999999999a", 0, "99999999999999999999", 20},
		{"007", 0, "7", 3},
	}
	for _, c := range cases {
		v, next := ReadCount(c.in, c.off)
		if v.String() != c.want || next != c.next {
			t.Errorf("ReadCount(%q,%d) = (%q,%d), want (%q,%d)", c.in, c.off, v, next, c.want, c.next)
		}
	}
}
