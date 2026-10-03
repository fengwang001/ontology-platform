package conflict

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// The differential test replays randomly generated documents and operation
// sequences against the real implementation and the naive reference
// implementation in naive_test.go. Inputs, outputs and the rule that
// decides each step are logged with t.Logf (visible with -v).

var markerChars = []byte{'<', '|', '=', '>'}

func randWord(rng *rand.Rand) string {
	words := []string{"a", "b", "c", "p", "q", "x", "y", "", "foo", "bar baz"}
	return words[rng.Intn(len(words))]
}

// randBodyLine returns a line that is guaranteed to be body (never a
// marker line) for marker length L.
func randBodyLine(rng *rand.Rand, L int) string {
	switch rng.Intn(4) {
	case 0:
		// Run shorter or longer than L: always body.
		c := markerChars[rng.Intn(4)]
		n := L - 1
		if rng.Intn(2) == 0 {
			n = L + 1 + rng.Intn(2)
		}
		return strings.Repeat(string(c), n)
	case 1:
		// Exactly L marker chars followed by a non-space: body.
		c := markerChars[rng.Intn(4)]
		return strings.Repeat(string(c), L) + "z"
	default:
		return randWord(rng)
	}
}

func randLabel(rng *rand.Rand) string {
	labels := []string{"", "x", "y", "ours v1", "a b  c", "tail  ", "  lead"}
	return labels[rng.Intn(len(labels))]
}

// genValidDoc builds a random structurally valid document and renders it
// to text with marker length L.
func genValidDoc(rng *rand.Rand, L int) string {
	var sb strings.Builder
	bodyLines := func() {
		for k := rng.Intn(4); k > 0; k-- {
			sb.WriteString(randBodyLine(rng, L))
			sb.WriteByte('\n')
		}
	}
	blockLines := func(max int) []string {
		var out []string
		for k := rng.Intn(max + 1); k > 0; k-- {
			if rng.Intn(3) == 0 {
				out = append(out, randBodyLine(rng, L))
			} else {
				// Small alphabet to create shared prefixes/suffixes.
				out = append(out, []string{"a", "b", "c"}[rng.Intn(3)])
			}
		}
		return out
	}
	mark := func(c byte, label string) {
		sb.WriteString(strings.Repeat(string(c), L))
		if label != "" {
			sb.WriteByte(' ')
			sb.WriteString(label)
		}
		sb.WriteByte('\n')
	}
	for n := rng.Intn(4); n >= 0; n-- {
		bodyLines()
		if n == 0 {
			break
		}
		mark('<', randLabel(rng))
		for _, ln := range blockLines(3) {
			sb.WriteString(ln)
			sb.WriteByte('\n')
		}
		if rng.Intn(2) == 0 {
			mark('|', randLabel(rng))
			for _, ln := range blockLines(2) {
				sb.WriteString(ln)
				sb.WriteByte('\n')
			}
		}
		mark('=', randLabel(rng))
		for _, ln := range blockLines(3) {
			sb.WriteString(ln)
			sb.WriteByte('\n')
		}
		mark('>', randLabel(rng))
	}
	return sb.String()
}

// genSoup builds a random unstructured line soup, often invalid.
func genSoup(rng *rand.Rand, L int) string {
	var sb strings.Builder
	for n := rng.Intn(10); n >= 0; n-- {
		switch rng.Intn(5) {
		case 0, 1:
			// A syntactic marker line (may be structurally invalid).
			c := markerChars[rng.Intn(4)]
			sb.WriteString(strings.Repeat(string(c), L))
			if lab := randLabel(rng); lab != "" {
				sb.WriteByte(' ')
				sb.WriteString(lab)
			}
		case 2:
			sb.WriteString(randBodyLine(rng, L))
		default:
			sb.WriteString(randWord(rng))
		}
		sb.WriteByte('\n')
	}
	s := sb.String()
	// Occasionally drop the final newline.
	if len(s) > 0 && rng.Intn(6) == 0 {
		s = s[:len(s)-1]
	}
	return s
}

func randMaxLines(rng *rand.Rand) int {
	picks := []int{1, 3, 5, 8, 12, 20, 40, 1000000, 1000000, 1000000}
	return picks[rng.Intn(len(picks))]
}

func choiceName(c Choice) string {
	switch c {
	case Ours:
		return "Ours"
	case Theirs:
		return "Theirs"
	case Both:
		return "Both"
	case Base:
		return "Base"
	case Custom:
		return "Custom"
	}
	return fmt.Sprintf("Choice(%d)", int(c))
}

func errString(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

// markerLenOf computes L' for a rendered text, mirroring the Render rule.
func markerLenOf(text string) int {
	m := 0
	for _, ln := range strings.Split(text, "\n") {
		if r := leadingRun(ln); r > m {
			m = r
		}
	}
	if m+1 > 7 {
		return m + 1
	}
	return 7
}

func TestDifferentialAgainstNaive(t *testing.T) {
	const cases = 2000
	for seed := int64(0); seed < cases; seed++ {
		rng := rand.New(rand.NewSource(seed))
		L := 7 + rng.Intn(3)
		maxLines := randMaxLines(rng)
		mode := "valid"
		var text string
		if rng.Intn(2) == 0 {
			text = genValidDoc(rng, L)
		} else {
			mode = "soup"
			text = genSoup(rng, L)
		}

		t.Logf("case %d mode=%s L=%d maxLines=%d input=%q", seed, mode, L, maxLines, text)

		realDoc, realErr := Parse(text, L, maxLines)
		naiveDoc, naiveErr := nParse(text, L, maxLines)

		if (realErr == nil) != (naiveErr == nil) {
			t.Fatalf("case %d: parse error mismatch: real=%v naive=%v input=%q",
				seed, realErr, naiveErr, text)
		}
		if realErr != nil {
			re, ne := realErr.(*Error), naiveErr.(*Error)
			if re.Kind != ne.Kind || re.Line != ne.Line {
				t.Fatalf("case %d: parse error mismatch: real=%v naive=%v input=%q",
					seed, realErr, naiveErr, text)
			}
			t.Logf("case %d: both rejected with %v (依据: 语法判定先于大小判定, 行号取最小)", seed, realErr)
			continue
		}

		if got, want := realDoc.Render(), naiveDoc.render(); got != want {
			t.Fatalf("case %d: initial render mismatch:\nreal =%q\nnaive=%q\ninput=%q",
				seed, got, want, text)
		}

		// Roundtrip: Parse(Render(), L') must render identically.
		rendered := realDoc.Render()
		Lp := markerLenOf(rendered)
		rt, err := Parse(rendered, Lp, 1000000)
		if err != nil {
			t.Fatalf("case %d: roundtrip Parse(%q, %d) failed: %v", seed, rendered, Lp, err)
		}
		if got := rt.Render(); got != rendered {
			t.Fatalf("case %d: roundtrip render mismatch: %q vs %q", seed, got, rendered)
		}

		// Replay a random operation sequence on both implementations.
		ops := 1 + rng.Intn(12)
		for op := 0; op < ops; op++ {
			blocks := realDoc.Blocks()
			if blocks != naiveDoc.blocks() {
				t.Fatalf("case %d op %d: Blocks mismatch: real=%d naive=%d",
					seed, op, blocks, naiveDoc.blocks())
			}
			switch rng.Intn(10) {
			case 0, 1:
				realDoc.Normalize()
				naiveDoc.normalize()
				t.Logf("case %d op %d: Normalize (依据: 先前缀后后缀, base 条件裁剪)", seed, op)
			case 2, 3:
				t.Logf("case %d op %d: checkpoint blocks=%d", seed, op, blocks)
			default:
				i := rng.Intn(blocks+2) - 1
				choice := Choice(rng.Intn(5))
				var custom []string
				if choice == Custom {
					for k := rng.Intn(6); k >= 0; k-- {
						if rng.Intn(8) == 0 {
							custom = append(custom, "bad\nline")
						} else {
							custom = append(custom, randBodyLine(rng, L))
						}
					}
				}
				realErr := realDoc.Resolve(i, choice, custom)
				naiveErr := naiveDoc.resolve(i, choice, custom)
				if (realErr == nil) != (naiveErr == nil) {
					t.Fatalf("case %d op %d: resolve error mismatch: real=%v naive=%v "+
						"(i=%d choice=%s custom=%q)", seed, op, realErr, naiveErr, i, choiceName(choice), custom)
				}
				if realErr != nil {
					if realErr.(*Error).Kind != naiveErr.(*Error).Kind {
						t.Fatalf("case %d op %d: resolve error kind mismatch: real=%v naive=%v "+
							"(i=%d choice=%s custom=%q)", seed, op, realErr, naiveErr, i, choiceName(choice), custom)
					}
				}
				t.Logf("case %d op %d: Resolve(i=%d choice=%s custom=%q) -> err=%s "+
					"(依据: ErrNoSuchBlock>ErrNoBase>ErrBadLine>ErrTooLarge, 拒绝不改状态)",
					seed, op, i, choiceName(choice), custom, errString(realErr))
			}
			if got, want := realDoc.Render(), naiveDoc.render(); got != want {
				t.Fatalf("case %d op %d: render mismatch:\nreal =%q\nnaive=%q\ninput=%q",
					seed, op, got, want, text)
			}
			t.Logf("case %d op %d: render=%q", seed, op, realDoc.Render())
		}
	}
}

// Replaying the same operation sequence twice must produce identical text
// and errors.
func TestReplayDeterminism(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		run := func() (string, []string) {
			rng := rand.New(rand.NewSource(seed))
			L := 7 + rng.Intn(3)
			text := genValidDoc(rng, L)
			d, err := Parse(text, L, 1000000)
			if err != nil {
				t.Fatalf("seed %d: unexpected parse error: %v", seed, err)
			}
			var errs []string
			for op := 0; op < 20; op++ {
				switch rng.Intn(4) {
				case 0:
					d.Normalize()
				default:
					i := rng.Intn(d.Blocks()+2) - 1
					choice := Choice(rng.Intn(5))
					var custom []string
					if choice == Custom {
						custom = []string{randWord(rng)}
					}
					errs = append(errs, errString(d.Resolve(i, choice, custom)))
				}
			}
			return d.Render(), errs
		}
		out1, errs1 := run()
		out2, errs2 := run()
		if out1 != out2 {
			t.Fatalf("seed %d: replay produced different text:\n%q\n%q", seed, out1, out2)
		}
		if fmt.Sprint(errs1) != fmt.Sprint(errs2) {
			t.Fatalf("seed %d: replay produced different errors:\n%v\n%v", seed, errs1, errs2)
		}
	}
}
