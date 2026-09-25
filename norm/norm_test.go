package norm

import (
	"math/rand"
	"sort"
	"testing"
)

// —— 朴素参照：与生产实现控制流不同，显式 sort.SliceStable + 组合全量重扫 ——
func refDecomp(r rune) []rune {
	if sBase <= r && r <= sMax {
		s := int(r) - sBase
		out := []rune{rune(lBase + s/588), rune(vBase + (s%588)/28)}
		if t := s % 28; t > 0 {
			out = append(out, rune(tBase+t))
		}
		return out
	}
	if d, ok := decomp[r]; ok {
		var out []rune
		for _, x := range d {
			out = append(out, refDecomp(x)...)
		}
		return out
	}
	return []rune{r}
}
func refNFD(s string) string {
	var d []rune
	for _, r := range s {
		d = append(d, refDecomp(r)...)
	}
	for lo := 0; lo < len(d); lo++ {
		if ccc(d[lo]) == 0 {
			continue
		}
		hi := lo
		for hi < len(d) && ccc(d[hi]) > 0 {
			hi++
		}
		sort.SliceStable(d[lo:hi], func(a, b int) bool { return ccc(d[lo+a]) < ccc(d[lo+b]) })
		lo = hi - 1
	}
	return string(d)
}

func refNFC(s string) string {
	var out []rune
	for _, r := range refNFD(s) {
		out = append(out, r)
		if len(out) < 2 {
			continue
		}
		if ccc(r) == 0 { // 韩文算法组合：仅与紧邻前一码点尝试
			a, b := out[len(out)-2], r
			switch {
			case lBase <= a && a <= lMax && vBase <= b && b <= vMax:
				out = append(out[:len(out)-2], rune(sBase+((int(a)-lBase)*21+int(b)-vBase)*28))
			case isLV(a) && tBase+1 <= b && b <= tMax:
				out = append(out[:len(out)-2], a+b-tBase)
			}
			continue
		}
		si := len(out) - 2 // 全量回扫找本段起始符
		for si >= 0 && ccc(out[si]) > 0 {
			si--
		}
		blocked := si < 0 // 全量重扫：S 与 M 间每个 B 都须 ccc(B) < ccc(M)
		for _, x := range out[si+1 : len(out)-1] {
			blocked = blocked || ccc(x) >= ccc(r)
		}
		if !blocked {
			if cp, ok := compose[pair{out[si], r}]; ok {
				out[si] = cp
				out = out[:len(out)-1]
			}
		}
	}
	return string(out)
}

func fuzzInputs(seed int64, n int) []string {
	rng := rand.New(rand.NewSource(seed))
	var alpha []rune
	for r := 'a'; r <= 'z'; r++ {
		alpha = append(alpha, r, r-'a'+'A')
	}
	alpha = append(alpha, 0x0300, 0x0301, 0x0308, 0x030A, 0x0323, 0x0327)
	for r := range decomp {
		alpha = append(alpha, r)
	}
	alpha = append(alpha, 0x1100, 0x1101, 0x1112, 0x1161, 0x1175, 0x11A8, 0x11C2, 0xAC00, 0xAC01, 0xAC1C, 0xD7A3)
	out := make([]string, n)
	for i := range out {
		rs := make([]rune, rng.Intn(9))
		for j := range rs {
			rs[j] = alpha[rng.Intn(len(alpha))]
		}
		out[i] = string(rs)
	}
	return out
}

func TestNaiveReference(t *testing.T) {
	for _, s := range fuzzInputs(20260926, 20000) {
		if got, ref := must(t, s, NFD), refNFD(s); got != ref {
			t.Fatalf("NFD(%q) = %q, ref %q", s, got, ref)
		}
		if got, ref := must(t, s, NFC), refNFC(s); got != ref {
			t.Fatalf("NFC(%q) = %q, ref %q", s, got, ref)
		}
	}
}

func must(t *testing.T, s string, f func(string) (string, error)) string {
	t.Helper()
	r, err := f(s)
	if err != nil {
		t.Fatalf("%q: %v", s, err)
	}
	return r
}

func TestIdempotent(t *testing.T) {
	for _, s := range fuzzInputs(20260926, 20000) {
		n1, c1 := must(t, s, NFD), must(t, s, NFC)
		if must(t, n1, NFD) != n1 || must(t, c1, NFC) != c1 || must(t, c1, NFD) != n1 {
			t.Fatalf("not idempotent / not equivalent: %q", s)
		}
	}
}

// TestEightInputs 钉住 NOTES.md 第三节的八行推导表。
func TestEightInputs(t *testing.T) {
	in := []string{"\u00e9", "e\u0301", "e\u0301\u0323", "e\u0323\u0301",
		"\u1100\u1161", "\uac00", "\u212b", "\u00c5"}
	want := []string{"\u00e9", "\u00e9", "\u1eb9\u0301", "\u1eb9\u0301",
		"\uac00", "\uac00", "\u00c5", "\u00c5"}
	keys := map[string]bool{}
	for i, s := range in {
		k, err := Key(s)
		if err != nil || k != want[i] {
			t.Fatalf("input %d %q: key %q err %v, want %q", i, s, k, err, want[i])
		}
		keys[k] = true
	}
	if len(keys) != 4 {
		t.Fatalf("distinct keys = %d, want 4", len(keys))
	}
}
