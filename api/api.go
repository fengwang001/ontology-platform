// Package api 是对外门面：包装 idx，并提供四条不变量的自检。
package api

import (
	"errors"
	"fmt"
	"sort"

	"ontology/idx"
	"ontology/norm"
)

// Index 规范等价键索引门面。
type Index struct{ in *idx.Index }

func New(maxKeys int) (*Index, error) {
	in, err := idx.New(maxKeys)
	if err != nil {
		return nil, err
	}
	return &Index{in: in}, nil
}

func (a *Index) Put(s string) error             { return a.in.Put(s) }
func (a *Index) Get(s string) ([]string, error) { return a.in.Get(s) }
func (a *Index) Distinct() int                  { return a.in.Distinct() }

// 内置自检样本：预组合/分解写法、乱序组合标记、韩文两种写法、组合排除。
var samples = []string{
	"\u00e9", "e\u0301", "\u00e8", "e\u0300", "\u00c7", "C\u0327", "\u00c4", "A\u0308",
	"e\u0301\u0323", "e\u0323\u0301", "\u1eb9\u0301",
	"\uac00", "\u1100\u1161", "\uac01", "\u1100\u1161\u11a8", "\u212b", "\u00c5", "A\u030a",
}

// —— 朴素参照：逐码点分解，显式 sort.SliceStable 分段排序，组合全量重扫 ——

var ncccTab = map[rune]int{0x0327: 202, 0x0323: 220, 0x0300: 230, 0x0301: 230, 0x0308: 230, 0x030A: 230}

func nccc(r rune) int { return ncccTab[r] }

func naiveNFD(s string) string {
	var d []rune
	for _, r := range s {
		d1, _ := norm.NFD(string(r)) // 单码点分解，与排序无关
		d = append(d, []rune(d1)...)
	}
	for lo := 0; lo < len(d); lo++ {
		if nccc(d[lo]) == 0 {
			continue
		}
		hi := lo
		for hi < len(d) && nccc(d[hi]) > 0 {
			hi++
		}
		sort.SliceStable(d[lo:hi], func(a, b int) bool { return nccc(d[lo+a]) < nccc(d[lo+b]) })
		lo = hi - 1
	}
	return string(d)
}

func pairNFC(a, b rune) string { s, _ := norm.NFC(string([]rune{a, b})); return s }

func naiveNFC(s string) string {
	var out []rune
	for _, r := range naiveNFD(s) {
		out = append(out, r)
		if len(out) < 2 {
			continue
		}
		if nccc(r) == 0 { // 韩文算法组合：仅与紧邻前一码点尝试
			if two := []rune(pairNFC(out[len(out)-2], r)); len(two) == 1 {
				out = append(out[:len(out)-2], two[0])
			}
			continue
		}
		si := len(out) - 2 // 全量回扫找本段起始符
		for si >= 0 && nccc(out[si]) > 0 {
			si--
		}
		blocked := si < 0 // 全量重扫：S 与 M 间每个 B 都须 ccc(B) < ccc(M)
		for _, b := range out[si+1 : len(out)-1] {
			blocked = blocked || nccc(b) >= nccc(r)
		}
		if !blocked {
			if two := []rune(pairNFC(out[si], r)); len(two) == 1 {
				out[si] = two[0]
				out = out[:len(out)-1]
			}
		}
	}
	return string(out)
}

// SelfCheck 对内置序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	a, _ := New(len(samples) + 1)
	cls := map[string]int{}
	for _, s := range samples {
		nfd, _ := norm.NFD(s)
		nfc, _ := norm.NFC(s)
		if naiveNFD(s) != nfd || naiveNFC(s) != nfc { // 不变量1：与朴素参照一致
			return fmt.Errorf("selfcheck: naive mismatch %q", s)
		}
		n2, _ := norm.NFD(nfd) // 不变量2：幂等且规范等价
		c2, _ := norm.NFC(nfc)
		if d2, _ := norm.NFD(nfc); n2 != nfd || c2 != nfc || d2 != nfd {
			return fmt.Errorf("selfcheck: not idempotent %q", s)
		}
		if err := a.Put(s); err != nil {
			return err
		}
		k, _ := norm.Key(s)
		cls[k]++
	}
	for _, s := range samples { // 不变量3：等价键一致，Get 精确返回等价类
		k, _ := norm.Key(s)
		got, _ := a.Get(s)
		if len(got) != cls[k] { // 桶内串去重且须全部同键，数量对即等价类对
			return fmt.Errorf("selfcheck: Get(%q) wrong class", s)
		}
		for _, g := range got {
			if gk, _ := norm.Key(g); gk != k {
				return fmt.Errorf("selfcheck: Get(%q) alien %q", s, g)
			}
		}
	}
	if a.Distinct() != len(cls) {
		return errors.New("selfcheck: Distinct mismatch")
	}
	b, _ := New(1) // 不变量4：失败不留痕
	if err := b.Put("\u00e9"); err != nil {
		return err
	}
	cases := []struct{ got, want error }{
		{b.Put("\u00c5"), idx.ErrTooManyKeys},
		{func() error { _, e := New(0); return e }(), idx.ErrBadMaxKeys},
		{b.Put("\xff"), norm.ErrInvalidUTF8},
		{b.Put("0"), norm.ErrUnsupported},
	}
	for _, c := range cases {
		if !errors.Is(c.got, c.want) {
			return fmt.Errorf("selfcheck: want %v, got %v", c.want, c.got)
		}
	}
	got, _ := b.Get("e\u0301")
	if b.Distinct() != 1 || len(got) != 1 || got[0] != "\u00e9" {
		return errors.New("selfcheck: state changed after rejection")
	}
	return nil
}
