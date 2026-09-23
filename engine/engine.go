// Package engine 在编译后的 syntax.Pattern 上执行匹配。
//
// 段内用单回退点的线性扫描（* 不跨段），跨段用段级 NFA 状态集合（**）。
// 时间复杂度 O(模式长度 × 路径长度)，无指数回溯。
package engine

import (
	"strings"
	"sync/atomic"

	"ontology/runes"
	"ontology/syntax"
)

// steps 是非导出计数器：最近一次 Match 的基本比较步数（原子读写，竞态安全）。
var steps atomic.Int64

// LastSteps 返回最近一次 Match 记录的步数。
func LastSteps() int64 { return steps.Load() }

// Match 判定路径 path 是否命中模式 p。
func Match(p *syntax.Pattern, path string) bool {
	var n int64
	defer func() { steps.Store(n) }()

	pSegs := p.Segs
	tSegs := strings.Split(path, "/")

	// closure：下标 i 为 ** 段时，零段跳到 i+1（定点闭包）。
	closeSet := func(s map[int]struct{}) {
		changed := true
		for changed {
			changed = false
			for i := range s {
				if i < len(pSegs) && pSegs[i].DoubleStar {
					if _, ok := s[i+1]; !ok {
						s[i+1] = struct{}{}
						changed = true
					}
				}
			}
		}
	}

	cur := map[int]struct{}{0: {}}
	closeSet(cur)
	for _, ts := range tSegs {
		next := map[int]struct{}{}
		for i := range cur {
			n++
			if i == len(pSegs) {
				continue
			}
			if pSegs[i].DoubleStar {
				next[i] = struct{}{} // ** 消费当前段后停留
			} else if segMatch(pSegs[i].Atoms, ts, &n) {
				next[i+1] = struct{}{}
			}
		}
		closeSet(next)
		cur = next
		if len(cur) == 0 {
			return false
		}
	}
	_, ok := cur[len(pSegs)]
	return ok
}

// segMatch 在单个段内做线性通配匹配；每轮主循环计一步，写入 *n。
func segMatch(atoms []syntax.Atom, seg string, n *int64) bool {
	txt := runes.Split(seg)
	pi, ti := 0, 0
	starPi, starTi := -1, 0
	for ti < len(txt) {
		*n++
		if pi < len(atoms) && atomMatch(atoms[pi], txt[ti]) {
			pi++
			ti++
			continue
		}
		if pi < len(atoms) && atoms[pi].Kind == syntax.KindStar {
			starPi, starTi = pi, ti
			pi++
			continue
		}
		if starPi >= 0 {
			pi = starPi + 1
			starTi++
			ti = starTi
			continue
		}
		return false
	}
	// 文本耗尽：模式剩余必须全是 *。
	for pi < len(atoms) {
		if atoms[pi].Kind != syntax.KindStar {
			return false
		}
		pi++
	}
	return true
}

// atomMatch 判定一个原子是否匹配一个码点单位。
func atomMatch(a syntax.Atom, u runes.Unit) bool {
	switch a.Kind {
	case syntax.KindAny:
		return true // 段内码点，必非 '/'；非法字节按一个码点处理
	case syntax.KindLit:
		return a.Raw == u.Raw
	case syntax.KindChr:
		return a.Cls.Match(u.R)
	default:
		return false
	}
}
