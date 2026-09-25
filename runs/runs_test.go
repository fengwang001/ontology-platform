package runs

import (
	"errors"
	"slices"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		s    string
		want []Run
	}{
		{"", nil},
		{"a", []Run{{'a', 1}}},
		{"aaab", []Run{{'a', 3}, {'b', 1}}},
		{"éé", []Run{{'é', 2}}},
		{"é", []Run{{'e', 1}, {'́', 1}}},
		{"112", []Run{{'1', 2}, {'2', 1}}},
	}
	for _, c := range cases {
		if got := Split(c.s); !slices.Equal(got, c.want) {
			t.Errorf("Split(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestCount(t *testing.T) {
	good := map[string]string{"": "1", "2": "2", "10": "10", "99999999999999999999": "99999999999999999999"}
	for in, want := range good {
		if n, err := Count(in); err != nil || n.String() != want {
			t.Errorf("Count(%q) = %v, %v; want %s", in, n, err, want)
		}
	}
	bad := []struct {
		in  string
		err error
	}{{"0", ErrZeroCount}, {"00", ErrLeadingZero}, {"01", ErrLeadingZero}, {"1", ErrExplicitOne}}
	for _, c := range bad {
		if _, err := Count(c.in); !errors.Is(err, c.err) {
			t.Errorf("Count(%q) = %v, want %v", c.in, err, c.err)
		}
	}
}

func TestAppendCount(t *testing.T) {
	for n, want := range map[int]string{2: "2", 12: "12", 1000: "1000"} {
		if got := string(AppendCount(nil, n)); got != want {
			t.Errorf("AppendCount(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestRuneScanner(t *testing.T) {
	for _, s := range []string{"a", "é", "漢", "🙂", "́"} {
		var sc RuneScanner
		for i := 0; i < len(s)-1; i++ {
			if _, ok, err := sc.Feed(s[i]); ok || err != nil {
				t.Fatalf("Feed(%q byte %d) = ok=%v, err=%v", s, i, ok, err)
			}
		}
		if r, ok, err := sc.Feed(s[len(s)-1]); !ok || err != nil || r != []rune(s)[0] {
			t.Fatalf("Feed(%q last) = %U, ok=%v, err=%v", s, r, ok, err)
		}
	}
	bad := []string{"\xff", "\xc3(", "\xed\xa0\x80", "\xf5\x80\x80\x80", "\x80", "\xc0\xaf"}
	for _, s := range bad {
		var sc RuneScanner
		var err error
		for i := 0; i < len(s) && err == nil; i++ {
			_, _, err = sc.Feed(s[i])
		}
		if !errors.Is(err, ErrInvalidUTF8) {
			t.Errorf("Feed(%q) err = %v, want ErrInvalidUTF8", s, err)
		}
	}
	var sc RuneScanner
	if _, ok, _ := sc.Feed(0xC3); ok || !sc.Pending() {
		t.Error("truncated lead byte should stay pending")
	}
}
