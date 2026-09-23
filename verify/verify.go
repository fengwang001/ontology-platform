// Package verify 校验段损坏分类与层级不变量。
package verify

import (
	"ontology/level"
	"ontology/segment"
	"sort"
)

// CheckSegment 打开段文件，返回 nil 或四类可判定错误之一：
// 头部不完整 / 条目不完整 / 索引段不完整 / CRC 不匹配。
func CheckSegment(path string) error {
	r, err := segment.Open(path)
	if err != nil {
		return err
	}
	return r.Close()
}

// Overlap 描述 L1+ 层内两段的键范围重叠。
type Overlap struct {
	Level  int
	A, B   string // 两段路径
	Lo, Hi string // 重叠区间 [Lo, Hi]
}

// CheckLevels 校验层级不变量：L1+ 层内段两两不重叠（L0 允许重叠）。
func CheckLevels(snap map[int][]level.Meta) []Overlap {
	var out []Overlap
	for lvl, ms := range snap {
		if lvl == 0 {
			continue
		}
		sorted := append([]level.Meta(nil), ms...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].MinKey < sorted[j].MinKey })
		for i := 1; i < len(sorted); i++ {
			prev, cur := sorted[i-1], sorted[i]
			if cur.MinKey <= prev.MaxKey {
				hi := prev.MaxKey
				if cur.MaxKey < hi {
					hi = cur.MaxKey
				}
				out = append(out, Overlap{Level: lvl, A: prev.Path, B: cur.Path, Lo: cur.MinKey, Hi: hi})
			}
		}
	}
	return out
}
