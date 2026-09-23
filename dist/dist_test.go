package dist

import (
	"errors"
	"strings"
	"testing"
)

func TestDistanceSamples(t *testing.T) {
	cases := []struct {
		name   string
		a, b   string
		expect int
	}{
		{"transpose caps", "CA", "AC", 1},
		{"transpose then insert", "CA", "ABC", 2},
		{"transpose lower", "ab", "ba", 1},
		{"transpose plus delete", "abc", "ca", 2},
		{"insert runes", "", "abc", 3},
		{"delete runes", "abc", "", 3},
		{"both empty", "", "", 0},
		{"composed codepoint", "é", "e", 1},
		{"cjk transpose", "日本", "本日", 1},
		{"distinct illegal bytes", "\xff", "\xfe", 1},
		{"illegal same", "\xff", "\xff", 0},
		{"legal replacement rune", string(rune(0xFFFD)), string(rune(0xFFFD)), 0},
		{"kitten", "kitten", "sitting", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Distance(c.a, c.b, 1<<62)
			if err != nil || got != c.expect {
				t.Fatalf("Distance(%q,%q)=%d,%v want %d", c.a, c.b, got, err, c.expect)
			}
		})
	}
}

func TestLimit(t *testing.T) {
	cases := []struct {
		name   string
		a, b   string
		limit  int64
		err    bool
		filled bool
	}{
		{"over limit", "abc", "abcd", 4, true, false},
		{"at limit", "abc", "ab", 6, false, true},
		{"zero nonempty", "a", "b", 0, true, false},
		{"zero empty", "", "", 0, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tb, err := Build(c.a, c.b, c.limit)
			if c.err != errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("err=%v want ErrLimitExceeded=%v", err, c.err)
			}
			if c.filled && tb == nil {
				t.Fatal("expected a table, got nil")
			}
		})
	}
}

func TestCellsCounter(t *testing.T) {
	cases := []struct {
		name        string
		m, n, bound int
	}{
		{"100x100", 100, 100, 2 * 101 * 101},
		{"1000x1000", 1000, 1000, 2 * 1001 * 1001},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tb, err := Build(strings.Repeat("a", c.m), strings.Repeat("b", c.n), 1<<62)
			if err != nil {
				t.Fatal(err)
			}
			if got := tb.CellsFilled(); got > int64(c.bound) {
				t.Fatalf("cells=%d > bound %d", got, c.bound)
			}
		})
	}
}

func TestIllegalBytesDistinct(t *testing.T) {
	cases := []struct {
		x, y byte
		want int
	}{
		{0xff, 0xfe, 1}, {0x80, 0xbf, 1}, {0xc0, 0xc1, 1}, {0xff, 0xff, 0},
	}
	for _, c := range cases {
		got, _ := Distance(string([]byte{c.x}), string([]byte{c.y}), 1<<62)
		if got != c.want {
			t.Fatalf("illegal %#x vs %#x = %d want %d", c.x, c.y, got, c.want)
		}
	}
}

func allStrings(alpha []rune, maxLen int) [][]rune {
	out := [][]rune{{}}
	frontier := [][]rune{{}}
	for len := 1; len <= maxLen; len++ {
		var next [][]rune
		for _, p := range frontier {
			for _, r := range alpha {
				q := append(append([]rune{}, p...), r)
				next = append(next, q)
				out = append(out, q)
			}
		}
		frontier = next
	}
	return out
}

func TestMetricOnTinyAlphabet(t *testing.T) {
	strs := allStrings([]rune{'a', 'b', 'c'}, 5)
	dm := make([][]int, len(strs))
	for i := range dm {
		dm[i] = make([]int, len(strs))
	}
	for i, sa := range strs {
		for j, sb := range strs {
			d, _ := Distance(string(sa), string(sb), 1<<62)
			dm[i][j] = d
			// identity of indiscernibles
			if (i == j) != (d == 0) {
				t.Fatalf("zero/equal mismatch at %d,%d d=%d", i, j, d)
			}
			// symmetry
			if i < j {
				e, _ := Distance(string(sb), string(sa), 1<<62)
				if d != e {
					t.Fatalf("asymmetric d(%q,%q)=%d vs %d", sa, sb, d, e)
				}
			}
		}
	}
	for i := range strs {
		for j := range strs {
			for k := range strs {
				if dm[i][k] > dm[i][j]+dm[j][k] {
					t.Fatalf("triangle violated d(%q,%q)=%d > %d+%d",
						strs[i], strs[k], dm[i][k], dm[i][j], dm[j][k])
				}
			}
		}
	}
}

// TestAlphabetIndependentMemory documents that the last-occurrence tables are
// map[rune]int keyed only by runes actually present in the inputs.
func TestAlphabetIndependentMemory(t *testing.T) {
	cases := [][2]string{
		{"ab", "cd"},
		{"好é", string([]rune{0x10000, 'z'})},
	}
	for _, c := range cases {
		tb, err := Build(c[0], c[1], 1<<62)
		if err != nil {
			t.Fatal(err)
		}
		// Same-length inputs over a much wider alphabet: the table size and
		// cell count stay identical; last-occurrence maps stay map[rune]int.
		if tb.CellsFilled() != 4 || tb.Value() != 2 {
			t.Fatalf("unexpected cells=%d value=%d", tb.CellsFilled(), tb.Value())
		}
	}
}
