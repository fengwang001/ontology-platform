package runs

import (
	"errors"
	"math"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []Run
	}{
		{"empty", "", nil},
		{"single", "a", []Run{{'a', 1}}},
		{"merge", "aaab", []Run{{'a', 3}, {'b', 1}}},
		{"digits", "1112", []Run{{'1', 3}, {'2', 1}}},
		{"multibyte", "éé中", []Run{{'é', 2}, {'中', 1}}},
		{"no normalization", "e\u0301e\u0301", []Run{{'e', 1}, {'\u0301', 1}, {'e', 1}, {'\u0301', 1}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Split(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("run %d: got %v, want %v", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestAppendCount(t *testing.T) {
	cases := []struct {
		count uint64
		want  string
	}{
		{1, ""},
		{2, "2"},
		{12, "12"},
		{math.MaxInt64, "9223372036854775807"},
	}
	for _, c := range cases {
		if got := string(AppendCount(nil, c.count)); got != c.want {
			t.Errorf("count %d: got %q, want %q", c.count, got, c.want)
		}
	}
}

func TestPushDigit(t *testing.T) {
	cases := []struct {
		name  string
		value uint64
		digit byte
		want  uint64
		err   bool
	}{
		{"first", 0, '3', 3, false},
		{"append", 3, '7', 37, false},
		{"zero", 0, '0', 0, false},
		{"max", math.MaxInt64 / 10, '7', math.MaxInt64, false},
		{"overflow digit", math.MaxInt64 / 10, '8', 0, true},
		{"overflow value", math.MaxInt64, '0', 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := PushDigit(c.value, c.digit)
			if c.err {
				if !errors.Is(err, ErrCountTooLarge) {
					t.Fatalf("got err %v, want ErrCountTooLarge", err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("got (%d, %v), want (%d, nil)", got, err, c.want)
			}
		})
	}
}
