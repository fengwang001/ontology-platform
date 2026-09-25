package hcode

import (
	"bytes"
	"cmp"
	"maps"
	"math/rand"
	"slices"
	"testing"

	"ontology/htree"
)

// naive 朴素参照：逐次合并最小频次（并列取字典序最小）得码长，再按 (码长,字典序) 赋规范码。
func naive(freq map[byte]int) (map[byte]int, map[byte]string) {
	type nd struct {
		f int
		n byte
		s []byte
	}
	var ns []nd
	for s, f := range freq {
		if f > 0 {
			ns = append(ns, nd{f, s, []byte{s}})
		}
	}
	lengths := map[byte]int{}
	for len(ns) > 1 {
		slices.SortFunc(ns, func(a, b nd) int {
			return cmp.Or(cmp.Compare(a.f, b.f), cmp.Compare(a.n, b.n))
		})
		s := append(ns[0].s, ns[1].s...)
		for _, x := range s {
			lengths[x]++
		}
		ns = append(ns[2:], nd{ns[0].f + ns[1].f, min(ns[0].n, ns[1].n), s})
	}
	if len(ns) == 1 && len(lengths) == 0 { // 单符号字母表码长为 1
		lengths[ns[0].n] = 1
	}
	syms := slices.Collect(maps.Keys(lengths))
	slices.SortFunc(syms, func(a, b byte) int {
		return cmp.Or(cmp.Compare(lengths[a], lengths[b]), cmp.Compare(a, b))
	})
	codes := map[byte]string{}
	code, prev := uint64(0), 0
	for _, s := range syms {
		l := lengths[s]
		code <<= uint(l - prev)
		prev = l
		b := make([]byte, l)
		for i, c := l-1, code; i >= 0; i, c = i-1, c>>1 {
			b[i] = byte('0' + c&1)
		}
		codes[s] = string(b)
		code++
	}
	return lengths, codes
}
func naivePack(codes map[byte]string, msg []byte) []byte {
	var out []byte
	cur, nb := byte(0), 0
	for _, s := range msg {
		for i := 0; i < len(codes[s]); i++ {
			cur = cur<<1 | (codes[s][i] - '0')
			if nb++; nb == 8 {
				out = append(out, cur)
				cur, nb = 0, 0
			}
		}
	}
	if nb > 0 {
		out = append(out, cur<<(8-nb))
	}
	return out
}
func freqTables() []map[byte]int {
	tabs := []map[byte]int{{'A': 5, 'B': 2, 'C': 1, 'D': 1, 'E': 1}, {'A': 1}}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 4; i++ {
		m := map[byte]int{}
		for j := 0; j < 2+r.Intn(30); j++ {
			m[byte(r.Intn(256))] = 1 + r.Intn(100)
		}
		tabs = append(tabs, m)
	}
	return tabs
}
func TestTreeLengthsNaive(t *testing.T) {
	for _, f := range freqTables() {
		nl, _ := naive(f)
		if got, err := htree.Lengths(f); err != nil || !maps.Equal(got, nl) {
			t.Fatalf("freq %v: got %v, err %v", f, got, err)
		}
	}
}

func TestCanonicalMatchesNaive(t *testing.T) {
	for _, f := range freqTables() {
		nl, nc := naive(f)
		tab := New(nl)
		for s, want := range nc {
			if got, _ := tab.CodeOf(s); got != want {
				t.Fatalf("freq %v sym %q: got %q want %q", f, s, got, want)
			}
		}
	}
}

func TestEncodeMatchesNaive(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for _, f := range freqTables() {
		nl, nc := naive(f)
		tab := New(nl)
		alphabet := slices.Collect(maps.Keys(nc))
		for k := 0; k < 10; k++ {
			var msg []byte
			for i := 0; i < r.Intn(40); i++ {
				msg = append(msg, alphabet[r.Intn(len(alphabet))])
			}
			got, err := tab.Encode(msg)
			if want := naivePack(nc, msg); err != nil || !bytes.Equal(got, want) {
				t.Fatalf("freq %v msg %v: got %v want %v", f, msg, got, want)
			}
		}
	}
}

func TestDecodeChecksBoundedByMaxLen(t *testing.T) {
	for _, m := range []int{100, 150, 200, 256} { // byte 字母表上限 256
		freq := map[byte]int{}
		for i := 0; i < m; i++ {
			freq[byte(i)] = i + 1
		}
		lengths, _ := htree.Lengths(freq)
		tab := New(lengths)
		if tab.maxLen > 32 { // 与 m 无关的常数上界，不随 m 线性增长
			t.Fatalf("m=%d: maxLen %d grows with m", m, tab.maxLen)
		}
		for s := 0; s < m; s++ { // 逐符号：检查条目数 ≤ 最长码长
			enc, _ := tab.Encode([]byte{byte(s)})
			if _, err := tab.Decode(enc, 1); err != nil {
				t.Fatal(err)
			}
			if got := tab.lastChecks.Load(); got > int64(tab.maxLen) {
				t.Fatalf("m=%d sym=%d: %d checks > maxLen %d", m, s, got, tab.maxLen)
			}
		}
	}
}
