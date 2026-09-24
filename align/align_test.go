package align_test

import (
	"errors"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"ontology/align"
	"ontology/dist"
)

func checkScript(t *testing.T, a, b string) {
	t.Helper()
	d, err := dist.Distance(a, b, 0)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := align.Script(a, b, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc) != d {
		t.Fatalf("d(%q,%q)=%d but script len=%d", a, b, d, len(sc))
	}
	out, err := align.Apply(a, sc)
	if err != nil || out != b {
		t.Fatalf("apply(%q)=%q,%v want %q", a, out, err, b)
	}
	cur := dist.Decode(a) // 逐步回放，验证转置作用于当时相邻的两码点
	for _, op := range sc {
		switch op.Kind {
		case align.Sub:
			cur[op.Pos] = op.R
		case align.Del:
			cur = append(cur[:op.Pos], cur[op.Pos+1:]...)
		case align.Ins:
			cur = append(cur[:op.Pos], append([]rune{op.R}, cur[op.Pos:]...)...)
		case align.Tr:
			if op.Pos < 0 || op.Pos+1 >= len(cur) {
				t.Fatalf("transpose at %d not adjacent, len=%d", op.Pos, len(cur))
			}
			cur[op.Pos], cur[op.Pos+1] = cur[op.Pos+1], cur[op.Pos]
		}
	}
}

func TestScriptTable(t *testing.T) {
	cases := [][2]string{
		{"CA", "AC"}, {"CA", "ABC"}, {"ab", "ba"}, {"abc", "ca"},
		{"", "abc"}, {"abc", ""}, {"", ""},
		{"é", "e"}, {"日本", "本日"}, {"\xff", "\xfe"},
		{"kitten", "sitting"}, {"a cat", "an act"}, {"abcdef", "azced"},
	}
	for _, c := range cases {
		checkScript(t, c[0], c[1])
	}
}

func TestScriptExhaustive(t *testing.T) {
	strs := gen([]rune("ab"), 4)
	for _, a := range strs {
		for _, b := range strs {
			checkScript(t, a, b)
		}
	}
	rng := rand.New(rand.NewSource(7))
	for k := 0; k < 300; k++ {
		checkScript(t, randStr(rng), randStr(rng))
	}
}

func gen(alphabet []rune, maxLen int) []string {
	var out []string
	var rec func(cur []rune)
	rec = func(cur []rune) {
		out = append(out, string(cur))
		if len(cur) == maxLen {
			return
		}
		for _, r := range alphabet {
			rec(append(cur, r))
		}
	}
	rec(nil)
	return out
}

func randStr(rng *rand.Rand) string {
	n := rng.Intn(9)
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteByte(byte('a' + rng.Intn(3)))
	}
	return sb.String()
}

func TestDeterministic(t *testing.T) {
	base, err := align.Script("determinism", "deterrence", 0)
	if err != nil {
		t.Fatal(err)
	}
	for k := 0; k < 100; k++ {
		sc, _ := align.Script("determinism", "deterrence", 0)
		if !reflect.DeepEqual(base, sc) {
			t.Fatalf("run %d differs", k)
		}
	}
}

func TestScriptLimit(t *testing.T) {
	_, err := align.Script(strings.Repeat("a", 10), strings.Repeat("b", 10), 50)
	if !errors.Is(err, dist.ErrLimitExceeded) {
		t.Fatalf("want ErrLimitExceeded, got %v", err)
	}
}

func TestApplyInvalid(t *testing.T) {
	bad := [][]align.Op{
		{{Kind: align.Del, Pos: 5}},
		{{Kind: align.Tr, Pos: 1}},
		{{Kind: align.Ins, Pos: -1}},
	}
	for _, sc := range bad {
		if _, err := align.Apply("ab", sc); !errors.Is(err, align.ErrInvalidScript) {
			t.Fatalf("want ErrInvalidScript, got %v", err)
		}
	}
}
