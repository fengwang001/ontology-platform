// Package align 从 dist 的距离表回溯出最优编辑脚本并可回放。
package align

import (
	"errors"

	"ontology/dist"
)

// Kind 是编辑脚本中的操作种类。
type Kind int

const (
	Sub Kind = iota // 替换 Pos 处码点为 R
	Del             // 删除 Pos 处码点
	Ins             // 在 Pos 处插入 R
	Tr              // 交换 Pos 与 Pos+1 两处相邻码点
)

// Op 是脚本中的一条操作；Pos 始终是执行该操作时串上的绝对下标。
type Op struct {
	Kind Kind
	Pos  int
	R    rune
}

// ErrInvalidScript 在脚本无法应用于给定源串时返回。
var ErrInvalidScript = errors.New("align: invalid script for source")

// kind 仅用于回溯阶段描述相对操作。
type kind int

const (
	kKeep kind = iota
	kSub
	kDel
	kIns
	kTrans
)

type step struct {
	k        kind
	r        rune // Ins/Sub 的目标码点
	i1, j1   int  // kTrans 的锚点
	gapS     int  // kTrans 的源间隙删除数 i-i1-1
	gapTFrom int  // kTrans 的目标间隙插入区间 [j1+1, j)
	gapTTo   int
}

// Script 返回 a 到 b 的一条最优编辑脚本。
// 并列选择规则固定：匹配 > 替换 > 删除 > 插入 > 转置。
func Script(a, b string, maxProduct int) ([]Op, error) {
	res, err := dist.Compute(a, b, maxProduct)
	if err != nil {
		return nil, err
	}
	ra, rb := res.Runes()
	var rev []step
	i, j := len(ra), len(rb)
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && ra[i-1] == rb[j-1] &&
			res.D(i, j) == res.D(i-1, j-1):
			rev = append(rev, step{k: kKeep})
			i--
			j--
		case i > 0 && j > 0 && res.D(i, j) == res.D(i-1, j-1)+1:
			rev = append(rev, step{k: kSub, r: rb[j-1]})
			i--
			j--
		case i > 0 && res.D(i, j) == res.D(i-1, j)+1:
			rev = append(rev, step{k: kDel})
			i--
		case j > 0 && res.D(i, j) == res.D(i, j-1)+1:
			rev = append(rev, step{k: kIns, r: rb[j-1]})
			j--
		default: // 转置：删源间隙、交换锚点、插目标间隙，之后仍可继续编辑
			i1, j1 := res.Trans(i, j)
			rev = append(rev, step{k: kTrans, i1: i1, j1: j1,
				gapS: i - i1 - 1, gapTFrom: j1 + 1, gapTTo: j})
			i, j = i1-1, j1-1
		}
	}
	// 正向重放：用游标给每条操作分配执行时刻的绝对下标。
	ops := make([]Op, 0, res.Dist())
	cur := 0
	for t := len(rev) - 1; t >= 0; t-- {
		switch rev[t].k {
		case kKeep:
			cur++
		case kSub:
			ops = append(ops, Op{Sub, cur, rev[t].r})
			cur++
		case kDel:
			ops = append(ops, Op{Del, cur, 0})
		case kIns:
			ops = append(ops, Op{Ins, cur, rev[t].r})
			cur++
		case kTrans:
			st := rev[t]
			for g := 0; g < st.gapS; g++ {
				ops = append(ops, Op{Del, cur + 1, 0})
			}
			ops = append(ops, Op{Tr, cur, 0})
			cur++ // 锚点 a[i] 匹配 b[j1]
			for k := st.gapTFrom; k < st.gapTTo; k++ {
				ops = append(ops, Op{Ins, cur, rb[k-1]})
				cur++
			}
			cur++ // 锚点 a[i1] 匹配 b[j]
		}
	}
	return ops, nil
}

// Apply 把脚本应用到 a，逐码点生成结果串。
func Apply(a string, script []Op) (string, error) {
	rs := dist.Decode(a)
	for _, op := range script {
		switch op.Kind {
		case Sub, Del, Tr:
			if op.Pos < 0 || op.Pos >= len(rs) {
				return "", ErrInvalidScript
			}
		case Ins:
			if op.Pos < 0 || op.Pos > len(rs) {
				return "", ErrInvalidScript
			}
		}
		switch op.Kind {
		case Sub:
			rs[op.Pos] = op.R
		case Del:
			rs = append(rs[:op.Pos], rs[op.Pos+1:]...)
		case Ins:
			rs = append(rs[:op.Pos], append([]rune{op.R}, rs[op.Pos:]...)...)
		case Tr:
			if op.Pos+1 >= len(rs) {
				return "", ErrInvalidScript
			}
			rs[op.Pos], rs[op.Pos+1] = rs[op.Pos+1], rs[op.Pos]
		}
	}
	return dist.Encode(rs), nil
}
