package ontology

import (
	"fmt"
	"testing"
	"unicode/utf8"
)

// naiveTokenize is the independent one-shot whole-string reference
// implementation that the streaming tokenizer is compared against.
func naiveTokenize(input string) ([]Token, Stats) {
	var toks []Token
	var cur []byte
	start, end, pos, reported, dropped := 0, 0, 0, 0, 0
	inToken := false
	consumed := 0

	flush := func() {
		if !inToken {
			return
		}
		if len(cur) > 64 {
			dropped++
		} else {
			toks = append(toks, Token{Text: string(cur), Start: start, End: end, Pos: pos})
			reported++
		}
		inToken = false
	}

	for offset := 0; len(input) > 0; {
		r, size := utf8.DecodeRuneInString(input)
		input = input[size:]
		charStart := offset
		charEnd := offset + size
		consumed += size
		offset += size

		out := naiveMap(r)
		if out == nil {
			if inToken {
				end = charEnd
			}
			continue
		}
		for _, b := range out {
			if isTokenByte(b) {
				if !inToken {
					inToken = true
					cur = cur[:0]
					start = charStart
					end = charEnd
					pos = reported + dropped
				}
				cur = append(cur, b)
				end = charEnd
			} else if inToken {
				flush()
			}
		}
	}
	flush()
	return toks, Stats{Tokens: reported, Dropped: dropped, Consumed: consumed}

}

func naiveMap(r rune) []byte {
	switch {
	case r >= 0x0300 && r <= 0x036F:
		return nil
	case r == 'ß':
		return []byte("ss")
	case r == 'æ' || r == 'Æ':
		return []byte("ae")
	case r == 'œ' || r == 'Œ':
		return []byte("oe")
	case r == 'ﬁ':
		return []byte("fi")
	case r == 'ﬂ':
		return []byte("fl")
	case r >= 'A' && r <= 'Z':
		return []byte{byte(r + ('a' - 'A'))}
	default:
		var buf [utf8.UTFMax]byte
		n := utf8.EncodeRune(buf[:], r)
		return buf[:n]
	}
}

// feedChunks feeds input in the given cut boundaries and collects tokens
// returned by Feed plus the final Close.
func feedChunks(t *testing.T, tz *Tokenizer, input []byte, cuts []int) []Token {
	t.Helper()
	var got []Token
	prev := 0
	for _, cut := range cuts {
		out, err := tz.Feed(input[prev:cut])
		if err != nil {
			t.Fatalf("unexpected feed error at cut %d: %v", cut, err)
		}
		got = append(got, out...)
		prev = cut
	}
	out, err := tz.Close()
	if err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}
	return append(got, out...)
}

func checkSame(t *testing.T, name, input string, got []Token, stats Stats) {
	t.Helper()
	want, wantStats := naiveTokenize(input)
	reason := "streaming output (tokens, positions, offsets, stats) equals naive one-pass output"
	if len(got) != len(want) {
		t.Fatalf("%s: token count mismatch\ninput=%q\ngot =%+v\nwant=%+v\nbasis=%s",
			name, input, got, want, reason)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: token %d mismatch\ninput=%q\ngot =%+v\nwant=%+v\nbasis=%s",
				name, i, input, got[i], want[i], reason)
		}
	}
	if stats != wantStats {
		t.Fatalf("%s: stats mismatch\ninput=%q\ngot =%+v\nwant=%+v\nbasis=%s",
			name, input, stats, wantStats, reason)
	}
	t.Logf("%s OK\n  input =%q\n  output=%+v\n  stats =%+v\n  basis =%s",
		name, input, got, stats, reason)
}

// runSplits compares a two-chunk feed at every possible cut (including
// cuts inside multibyte characters) and a byte-at-a-time feed against the
// naive one-shot result.
func runSplits(t *testing.T, input string) {
	t.Helper()
	raw := []byte(input)
	for cut := 0; cut <= len(raw); cut++ {
		tz := NewTokenizer()
		got := feedChunks(t, tz, raw, []int{cut, len(raw)})
		checkSame(t, fmt.Sprintf("two-chunks/cut=%d", cut), input, got, tz.Stats())
	}
	tz := NewTokenizer()
	cuts := make([]int, 0, len(raw)+1)
	for i := range raw {
		cuts = append(cuts, i)
	}
	cuts = append(cuts, len(raw))
	got := feedChunks(t, tz, raw, cuts)
	checkSame(t, "byte-at-a-time", input, got, tz.Stats())
}
