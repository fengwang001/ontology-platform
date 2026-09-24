// Package cycle 把有向环借助临时名展开成安全的线性操作序列。
package cycle

import (
	"errors"
	"fmt"

	"ontology/name"
	"ontology/plan"
)

// Op 是最终可线性执行的一步。IsTemp=true 表示最后一步把临时名搬到目标。
type Op struct {
	Old, New string
	IsTemp   bool
}

// ErrTempExhausted：所有形如临时名的候选都被占用（可判定，绝不覆盖数据）。
var ErrTempExhausted = errors.New("cycle: cannot allocate free temp name")

const maxTempTries = 1_000_000

// tempTaken 判断候选临时名是否会与现有集合或本批任何名字冲突。
func tempTaken(ns *name.Namespace, busy map[string]struct{}, tmp string) bool {
	if ns.HasLocked(tmp) {
		return true
	}
	_, ok := busy[tmp]
	return ok
}

// allocTemp 生成一个不与现有名/本批目标冲突的临时名，冲突即重试。
func allocTemp(ns *name.Namespace, busy map[string]struct{}, hint int) (string, error) {
	for n := hint; n < hint+maxTempTries; n++ {
		candidate := fmt.Sprintf(".rename.tmp-%d", n)
		if !tempTaken(ns, busy, candidate) {
			busy[candidate] = struct{}{}
			return candidate, nil
		}
	}
	return "", ErrTempExhausted
}

// Expand 把 plan 展开为线性操作：拓扑序在前，每个环用一个临时名破解。
// 返回的操作序列中，每一步执行时其目标名必然不存在。
func Expand(ns *name.Namespace, p *plan.Plan) ([]Op, int, error) {
	busy := make(map[string]struct{}, len(p.Reqs))
	for _, r := range p.Reqs {
		busy[r.Old] = struct{}{}
		busy[r.New] = struct{}{}
	}

	var ops []Op
	seq := 0
	hint := 0
	for _, r := range p.Order {
		ops = append(ops, Op{Old: r.Old, New: r.New})
	}
	// 环按“首边起点”字典序处理（plan.Build 已如此排序），保证确定性。
	for _, cyc := range p.Cycles {
		tmp, err := allocTemp(ns, busy, hint)
		if err != nil {
			return nil, seq, err
		}
		hint++
		seq++
		x0 := cyc[0].Old
		ops = append(ops, Op{Old: x0, New: tmp}) // 1) 搬出环上第一节点
		// 2) 沿环逆方向执行其余边：x_{k}->x_{k-1},...,x1->x0
		for i := len(cyc) - 1; i >= 1; i-- {
			ops = append(ops, Op{Old: cyc[i].Old, New: cyc[i-1].Old})
		}
		ops = append(ops, Op{Old: tmp, New: cyc[0].New, IsTemp: true}) // 3) 临时名落位
	}
	return ops, seq, nil
}
