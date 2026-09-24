// Package interval 提供左闭右开区间、+∞ 表示，以及按变更点生成历史行、
// 行的拆分与闭合、Eff 合法性校验。它不依赖项目中的其他包。
package interval

import (
	"errors"
	"math"
)

// Inf 表示 +∞。所有合法 Eff 都严格小于 Inf，因此任何行都满足 From < To。
const Inf int64 = math.MaxInt64

// ErrInvalidEff 是 Eff 越界（Eff < 0 或 Eff == Inf）的哨兵错误。
var ErrInvalidEff = errors.New("interval: effective time out of range [0, MaxInt64)")

// Op 是维度变更操作：Upsert 赋值，Delete 标记该键从此刻起不存在。
type Op string

const (
	Upsert Op = "upsert"
	Delete Op = "delete"
)

// Point 是一个变更点：从 Eff 起，键的值为 Val（Del=false）或键不存在（Del=true）。
type Point struct {
	Eff int64
	Del bool
	Val string
}

// Row 是一行历史 [From, To) -> Val；To == Inf 表示当前有效（开放行）。
type Row struct {
	From int64
	To   int64
	Val  string
}

// ValidEff 报告 eff 是否满足 0 <= eff < Inf。
func ValidEff(eff int64) bool { return 0 <= eff && eff < Inf }

// Build 把已按 Eff 升序排列、Eff 两两不同的变更点一次性生成历史行：
// 每个 Upsert 点产生 [它的 Eff, 下一个变更点的 Eff)（无下一个则为 +∞），
// Delete 点不产生行；相邻行即使 Val 相同也不合并。
func Build(pts []Point) []Row {
	rows := make([]Row, 0, len(pts))
	for i, p := range pts {
		if p.Del {
			continue
		}
		to := Inf
		if i+1 < len(pts) {
			to = pts[i+1].Eff
		}
		rows = append(rows, Row{From: p.Eff, To: to, Val: p.Val})
	}
	return rows
}

// SplitAt 在 t 处把行 r 拆成 [From,t) 与 [t,To)，两段 Val 相同。
// 仅当 From < t < To 时合法（空区间永不产生）。
func SplitAt(r Row, t int64) (left, right Row, ok bool) {
	if !(r.From < t && t < r.To) {
		return Row{}, Row{}, false
	}
	return Row{From: r.From, To: t, Val: r.Val}, Row{From: t, To: r.To, Val: r.Val}, true
}

// Close 把开放或跨越 t 的行闭合为 [From,t)。仅当 From < t < To 时合法。
func Close(r Row, t int64) (Row, bool) {
	if !(r.From < t && t < r.To) {
		return Row{}, false
	}
	return Row{From: r.From, To: t, Val: r.Val}, true
}
