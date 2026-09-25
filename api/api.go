// Package api 对外提供字素簇分段服务。依赖 grapheme。
package api

import (
	"errors"
	"unicode/utf8"

	"ontology/grapheme"
	"ontology/seg"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrEmpty      = errors.New("api: empty string")
	ErrOutOfRange = errors.New("api: cluster index out of range")
)

// Cluster 是一个字素簇：Start/End 为字节偏移（左闭右开），Runes 为码点数。
type Cluster = grapheme.Cluster

// Segmenter 无状态，所有方法可并发调用。
type Segmenter struct{}

// New 构造一个 Segmenter。
func New() *Segmenter { return &Segmenter{} }

// Segments 返回 s 的全部字素簇；空串与非法 UTF-8 整体失败，不留部分结果。
func (g *Segmenter) Segments(s string) ([]Cluster, error) {
	if s == "" {
		return nil, ErrEmpty
	}
	return grapheme.Decode(s)
}

// Count 返回 s 的簇数。
func (g *Segmenter) Count(s string) (int, error) {
	cs, err := g.Segments(s)
	if err != nil {
		return 0, err
	}
	return len(cs), nil
}

// At 返回第 i 个簇；i 越界时失败。
func (g *Segmenter) At(s string, i int) (Cluster, error) {
	cs, err := g.Segments(s)
	if err != nil {
		return Cluster{}, err
	}
	if i < 0 || i >= len(cs) {
		return Cluster{}, ErrOutOfRange
	}
	return cs[i], nil
}

// SelfCheck 对内置字符串核验四条不变量，全部通过返回 nil。
func (g *Segmenter) SelfCheck() error {
	for _, s := range []string{
		"éa‍b\r\n\U0001F1E6\U0001F1E7c👍🏻",
		"hello, 世界 👩‍💻 \r\nxy",
		"\U0001F1E6\U0001F1E7\U0001F1E8",
	} {
		if err := checkInvariants(s); err != nil {
			return err
		}
	}
	if _, err := g.Segments(""); !errors.Is(err, ErrEmpty) {
		return errors.New("selfcheck: empty string not rejected")
	}
	if _, err := g.Segments("\xff"); !errors.Is(err, grapheme.ErrInvalidUTF8) {
		return errors.New("selfcheck: invalid utf-8 not rejected")
	}
	if _, err := g.At("ab", 2); !errors.Is(err, ErrOutOfRange) {
		return errors.New("selfcheck: out of range not rejected")
	}
	return nil
}

// checkInvariants 用朴素参照（逐 rune 累加字节长）核验不变量 1–3。
func checkInvariants(s string) error {
	cs, err := grapheme.Decode(s)
	if err != nil {
		return err
	}
	var want []Cluster // 朴素参照：独立重算边界与偏移
	start, runes, off, riRun := 0, 0, 0, 0
	var prev rune
	for i, r := range []rune(s) {
		if i > 0 && !seg.NoBreak(prev, r, riRun) {
			want = append(want, Cluster{Start: start, End: off, Runes: runes})
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
	want = append(want, Cluster{Start: start, End: off, Runes: runes})
	if len(cs) != len(want) {
		return errors.New("selfcheck: cluster count mismatch vs naive")
	}
	for i := range want {
		if cs[i] != want[i] { // 不变量 1、2：边界与偏移逐簇相同
			return errors.New("selfcheck: cluster mismatch vs naive")
		}
		if i > 0 && cs[i-1].End != cs[i].Start { // 不变量 3：首尾相接
			return errors.New("selfcheck: offsets not chained")
		}
	}
	if cs[0].Start != 0 || cs[len(cs)-1].End != len(s) {
		return errors.New("selfcheck: end offsets not anchored")
	}
	return nil
}
