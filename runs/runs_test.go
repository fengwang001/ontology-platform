package runs

import (
	"math"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		in   string
		want []Run
	}{
		{"", nil},
		{"a", []Run{{'a', 1}}},
		{"aaab", []Run{{'a', 3}, {'b', 1}}},
		{"a1", []Run{{'a', 1}, {'1', 1}}},
		{"111", []Run{{'1', 3}}},
		{"éé", []Run{{'é', 2}}},
		{"é", []Run{{'e', 1}, {'\u0301', 1}}},
		{"abba", []Run{{'a', 1}, {'b', 2}, {'a', 1}}},
	}
	for _, c := range cases {
		got := Split(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("Split(%q) = %+v, want %+v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("Split(%q)[%d] = %+v, want %+v", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestAppendDigit(t *testing.T) {
	cases := []struct {
		n    int
		d    byte
		want int
		ok   bool
	}{
		{0, '0', 0, true},
		{0, '9', 9, true},
		{12, '3', 123, true},
		{MaxCount / 10, '7', MaxCount/10*10 + 7, MaxCount%10 >= 7},
		{MaxCount, '0', MaxCount, false},
	}
	for _, c := range cases {
		got, ok := AppendDigit(c.n, c.d)
		if ok != c.ok || ok && got != c.want {
			t.Fatalf("AppendDigit(%d,%q)=(%d,%v), want (%d,%v)", c.n, c.d, got, ok, c.want, c.ok)
		}
	}
}

func TestAppendCountRoundTrip(t *testing.T) {
	values := []int{1, 9, 10, 999, 123456789, math.MaxInt}
	for _, v := range values {
		b := AppendCount(nil, v)
		n := 0
		ok := true
		for _, c := range b {
			n, ok = AppendDigit(n, c)
			if !ok {
				t.Fatalf("AppendDigit overflow while replaying %d", v)
			}
		}
		if n != v {
			t.Fatalf("count roundtrip got %d, want %d", n, v)
		}
	}
}
