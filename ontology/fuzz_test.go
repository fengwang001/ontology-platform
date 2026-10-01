package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

// fuzzAlphabet covers every filter path: plain ASCII letters/digits,
// ASCII separators, folded ligatures (2- and 3-byte encodings), deleted
// combining marks, multi-byte non-ASCII token-adjacent characters
// (all bytes are separators) and 4-byte runes. Long runs of token
// characters exercise the 64/65-byte drop boundary too.
var fuzzAlphabet = []rune{
	'a', 'b', 'z', 'A', 'M', 'Z', '0', '9', ' ', '.', '-', '_', '!', '\n',
	'ß', 'æ', 'Æ', 'œ', 'Œ', 'ﬁ', 'ﬂ',
	0x0300, 0x0308, 0x036F, // deleted combining marks
	'é', '中', '€', '😀', // multi-byte non-ASCII separators
}

func randomValidUTF8(rng *rand.Rand) string {
	n := rng.Intn(60)
	rs := make([]rune, 0, n)
	for i := 0; i < n; i++ {
		// Occasionally force a long token run to cross 64 bytes.
		if i%17 == 5 {
			rs = append(rs, rune('a'+rng.Intn(26)))
		}
		rs = append(rs, fuzzAlphabet[rng.Intn(len(fuzzAlphabet))])
	}
	return string(rs)
}

// stream runs the tokenizer with a given feeding schedule and returns the
// concatenated token stream plus final stats.
func stream(t *testing.T, raw []byte, cuts []int) ([]Token, Stats) {
	t.Helper()
	tz := NewTokenizer()
	got := feedChunks(t, tz, raw, cuts)
	return got, tz.Stats()
}

func TestRandomDifferential2000(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	for iter := 0; iter < 2000; iter++ {
		input := randomValidUTF8(rng)
		raw := []byte(input)
		want, wantStats := naiveTokenize(input)

		schedules := map[string][]int{}
		// One shot.
		schedules["one-shot"] = []int{len(raw)}
		// Byte at a time.
		byteCuts := make([]int, 0, len(raw)+1)
		for i := range raw {
			byteCuts = append(byteCuts, i)
		}
		byteCuts = append(byteCuts, len(raw))
		schedules["byte-at-a-time"] = byteCuts
		// A handful of random cut points.
		randomCuts := []int{len(raw)}
		for k := 0; k < 3; k++ {
			randomCuts = append(randomCuts, rng.Intn(len(raw)+1))
		}
		for k := 0; k < len(randomCuts); k++ {
			for j := k + 1; j < len(randomCuts); j++ {
				if randomCuts[k] > randomCuts[j] {
					randomCuts[k], randomCuts[j] = randomCuts[j], randomCuts[k]
				}
			}
		}
		schedules["random-cuts"] = randomCuts
		// One exhaustive two-chunk split per iteration, at a random cut.
		cut := rng.Intn(len(raw) + 1)
		schedules[fmt.Sprintf("two-chunks@%d", cut)] = []int{cut, len(raw)}

		for name, cuts := range schedules {
			got, stats := stream(t, raw, cuts)
			basis := fmt.Sprintf(
				"case=%d schedule=%s: tokens/positions/offsets/stats identical across all split points and equal to naive one-pass",
				iter, name)
			if len(got) != len(want) {
				t.Fatalf("case %d %s: count mismatch\ninput=%q\ngot =%+v\nwant=%+v\nbasis=%s",
					iter, name, input, got, want, basis)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("case %d %s: token %d mismatch\ninput=%q\ngot =%+v\nwant=%+v\nbasis=%s",
						iter, name, i, input, got[i], want[i], basis)
				}
			}
			if stats != wantStats {
				t.Fatalf("case %d %s: stats mismatch\ninput=%q\ngot =%+v\nwant=%+v\nbasis=%s",
					iter, name, input, stats, wantStats, basis)
			}
		}

		if iter < 20 || testing.Verbose() {
			t.Logf("case=%d PASS\n  input =%q\n  output=%+v\n  stats =%+v\n  basis =one-shot, byte-at-a-time, random cuts and a two-chunk split all equal naive output",
				iter, input, want, wantStats)
		}
	}
	t.Logf("differential fuzz complete: 2000 valid random UTF-8 inputs, 4 schedules each")
}
