package runs

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestEach(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"a", "a:1"},
		{"aaab", "a:3 b:1"},
		{"aabbcc", "a:2 b:2 c:2"},
		{"ééa", "é:2 a:1"},
		{"e\u0301e\u0301", "e:1 \u0301:1 e:1 \u0301:1"},
		{"\\11", "\\:1 1:2"},
	}
	for _, c := range cases {
		var parts []string
		Each(c.in, func(r rune, n uint64) { parts = append(parts, fmt.Sprintf("%c:%d", r, n)) })
		if got := strings.Join(parts, " "); got != c.want {
			t.Errorf("Each(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAddDigit(t *testing.T) {
	cases := []struct {
		n, d, want uint64
		err        bool
	}{
		{0, 0, 0, false},
		{1, 2, 12, false},
		{math.MaxUint64 / 10, 5, math.MaxUint64, false},
		{math.MaxUint64 / 10, 6, 0, true},
		{math.MaxUint64, 0, 0, true},
	}
	for _, c := range cases {
		got, err := AddDigit(c.n, c.d)
		if c.err {
			if !errors.Is(err, ErrOverflow) {
				t.Errorf("AddDigit(%d,%d) err = %v, want ErrOverflow", c.n, c.d, err)
			}
		} else if err != nil || got != c.want {
			t.Errorf("AddDigit(%d,%d) = %d,%v, want %d", c.n, c.d, got, err, c.want)
		}
	}
}

func TestAppendDecimal(t *testing.T) {
	cases := []struct {
		n    uint64
		want string
	}{
		{0, "0"}, {2, "2"}, {12, "12"}, {math.MaxUint64, "18446744073709551615"},
	}
	for _, c := range cases {
		if got := string(AppendDecimal(nil, c.n)); got != c.want {
			t.Errorf("AppendDecimal(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
