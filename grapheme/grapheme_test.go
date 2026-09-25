package grapheme

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"ontology/seg"
)

// 钉住不变量 1、3 与线性复杂度：解码 rune 次数恰等于 m，回扫字节为 0。
func TestDecodeCountLinear(t *testing.T) {
	for _, m := range []int{100, 333, 1000, 5000, 10000} {
		s := strings.Repeat("aé👍́", m/4) + strings.Repeat("b", m%4)
		before := decoded.Load()
		cs, err := Decode(s)
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if got := decoded.Load() - before; got != int64(m) {
			t.Fatalf("m=%d: decoded runes = %d, want exactly %d", m, got, m)
		}
		if got := rescanned.Load(); got != 0 {
			t.Fatalf("m=%d: rescanned bytes = %d, want 0", m, got)
		}
		total := 0
		for _, c := range cs {
			total += c.Runes
		}
		if total != m {
			t.Fatalf("m=%d: cluster runes sum = %d", m, total)
		}
	}
}

// 朴素参照：从头逐个 rune 累加 UTF-8 字节长，独立重算边界。
func naive(s string) []Cluster {
	var out []Cluster
	start, runes, off, riRun := 0, 0, 0, 0
	var prev rune
	for i, r := range []rune(s) {
		if i > 0 && !seg.NoBreak(prev, r, riRun) {
			out = append(out, Cluster{start, off, runes})
			start, runes = off, 0
		}
		if seg.Regional(r) {
			riRun++
		} else {
			riRun = 0
		}
		prev, runes = r, runes+1
		off += utf8.RuneLen(r)
	}
	return append(out, Cluster{start, off, runes})
}

// 钉住不变量 1：表驱动 + 随机码点序列，逐簇与朴素参照相同。
func TestAgainstNaive(t *testing.T) {
	fixed := []string{
		"éa‍b\r\n\U0001F1E6\U0001F1E7c👍🏻",
		"\r\n", "\r\r", "\n\n",
		"\U0001F1E6\U0001F1E7\U0001F1E8\U0001F1E9\U0001F1EA",
		"á̂̃b", "x‍y‍z",
		"👍🏻👍🏿👍", "café‍",
	}
	rng := rand.New(rand.NewSource(42))
	pool := []rune("abé́‍\r\n\U0001F1E6\U0001F1E7\U0001F1E8👍🏻️")
	for k := 0; k < 200; k++ {
		var sb strings.Builder
		for j := 0; j < 1+rng.Intn(30); j++ {
			sb.WriteRune(pool[rng.Intn(len(pool))])
		}
		fixed = append(fixed, sb.String())
	}
	for _, s := range fixed {
		got, err := Decode(s)
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		want := naive(s)
		if len(got) != len(want) {
			t.Fatalf("%q: %d clusters, want %d", s, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%q: cluster %d = %+v, want %+v", s, i, got[i], want[i])
			}
		}
	}
}

// 非法 UTF-8：整体失败、可取出首个非法字节偏移、不产出部分簇。
func TestInvalidUTF8(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"\xff", 0}, {"ab\xffcd", 2}, {"ok\xe2\x28\xa1", 2}, {"fine\xed\xa0\x80", 4},
	}
	for _, c := range cases {
		cs, err := Decode(c.s)
		if cs != nil || err == nil {
			t.Fatalf("%q: got (%v, %v)", c.s, cs, err)
		}
		e, ok := err.(*InvalidUTF8Error)
		if !ok || e.Offset != c.want {
			t.Fatalf("%q: err = %#v, want offset %d", c.s, err, c.want)
		}
	}
}
