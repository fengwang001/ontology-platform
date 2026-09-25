package hcode

import (
	"bytes"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"testing"

	"ontology/htree"
)

func buildT(t *testing.T, freq map[byte]int) *Table {
	l, _ := htree.Lengths(freq) // test inputs are always valid
	return Build(l)
}
func randFreq(r *rand.Rand, m int) map[byte]int {
	f := map[byte]int{}
	for i := 0; i < m; i++ {
		f[byte(i)] = 1 + r.Intn(1000)
	}
	return f
}
func bitStr(c uint64, l int) string {
	s := strconv.FormatUint(c, 2)
	return strings.Repeat("0", l-len(s)) + s
}

// naiveEncode concatenates codeword bit strings, then packs: an
// independent reference for Table.Encode.
func naiveEncode(t *Table, msg []byte) []byte {
	var bits string
	for _, s := range msg {
		c, l, _ := t.Code(s)
		bits += bitStr(c, l)
	}
	var out []byte
	for i := 0; i < len(bits); i += 8 {
		var b byte
		for j := 0; j < 8 && i+j < len(bits); j++ {
			if bits[i+j] == '1' {
				b |= 1 << uint(7-j)
			}
		}
		out = append(out, b)
	}
	return out
}

func naiveDecode(tb *Table, b []byte, n int) []byte {
	inv := map[string]byte{}
	for s, l := range tb.lengths {
		inv[bitStr(tb.codes[s], l)] = s
	}
	out, cur := []byte(nil), ""
	for _, by := range b {
		for i := 7; i >= 0 && len(out) < n; i-- {
			cur += string('0' + by>>uint(i)&1)
			if s, ok := inv[cur]; ok {
				out = append(out, s)
				cur = ""
			}
		}
	}
	return out
}
func round(t *testing.T, r *rand.Rand) {
	for _, m := range []int{2, 5, 27, 256} {
		tb := buildT(t, randFreq(r, m))
		msg := make([]byte, 300)
		for i := range msg {
			msg[i] = byte(r.Intn(m))
		}
		enc, err := tb.Encode(msg)
		if err != nil || !bytes.Equal(enc, naiveEncode(tb, msg)) {
			t.Fatalf("m=%d: encode mismatch", m)
		}
		dec, err := tb.Decode(enc, len(msg))
		if err != nil || !bytes.Equal(dec, naiveDecode(tb, enc, len(msg))) || !bytes.Equal(dec, msg) {
			t.Fatalf("m=%d: decode mismatch", m)
		}
	}
}
func TestEncodeNaive(t *testing.T) { round(t, rand.New(rand.NewSource(1))) }
func TestDecodeNaive(t *testing.T) { round(t, rand.New(rand.NewSource(2))) }
func TestFeedChunkings(t *testing.T) {
	tb := buildT(t, map[byte]int{'A': 5, 'B': 2, 'C': 1, 'D': 1, 'E': 1})
	msg := []byte("AABCCDEDCBA")
	enc, _ := tb.Encode(msg)
	want, _ := tb.Decode(enc, len(msg))
	for c1 := 0; c1 <= len(enc); c1++ {
		for c2 := c1; c2 <= len(enc); c2++ {
			s := tb.NewStream(len(msg))
			s.Feed(enc[:c1])
			s.Feed(enc[c1:c2])
			s.Feed(enc[c2:])
			got, err := s.Finish()
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("cuts %d,%d: %v", c1, c2, err)
			}
		}
	}
}
func TestPrefixFree(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for _, m := range []int{2, 5, 100, 256} {
		tb := buildT(t, randFreq(r, m))
		var codes []string
		for s, l := range tb.lengths {
			codes = append(codes, bitStr(tb.codes[s], l))
		}
		sort.Strings(codes) // prefix collisions show up in adjacent pairs
		for i := 1; i < len(codes); i++ {
			if strings.HasPrefix(codes[i], codes[i-1]) {
				t.Fatalf("m=%d: %q prefixes %q", m, codes[i-1], codes[i])
			}
		}
	}
}
func TestDeterministic(t *testing.T) {
	freq := map[byte]int{'A': 5, 'B': 2, 'C': 1, 'D': 1, 'E': 1}
	a, b := buildT(t, freq), buildT(t, freq)
	for s := range freq {
		ca, la, _ := a.Code(s)
		cb, lb, _ := b.Code(s)
		if ca != cb || la != lb {
			t.Fatalf("symbol %c: codes differ", s)
		}
	}
}

// TestDecodeChecksBound: checks per symbol ≤ maxLen, independent of m.
func TestDecodeChecksBound(t *testing.T) {
	for _, m := range []int{100, 150, 200, 256} {
		freq := map[byte]int{}
		for i := 0; i < m; i++ {
			freq[byte(i)] = i + 1
		}
		tb := buildT(t, freq)
		for s := range freq {
			enc, _ := tb.Encode([]byte{s})
			st := tb.NewStream(1)
			st.Feed(enc)
			if st.checks > tb.maxLen || st.checks >= m {
				t.Fatalf("m=%d sym=%d: checks %d (maxLen %d)", m, s, st.checks, tb.maxLen)
			}
		}
	}
}
