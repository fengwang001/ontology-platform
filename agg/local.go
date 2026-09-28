package agg

import (
	"log"
	"strings"
)

// LocalAggregate 对一批行执行本地部分聚合。
//
// 校验规则（任一行失败则整批拒绝，不产出任何部分聚合）：
//  1. 操作必须是 add 或 withdraw，否则 ErrInvalidOperation；
//  2. 组名不允许为空，否则 ErrEmptyGroup。
//
// 对每个组产出和增量、计数增量与按值净增减映射；
// 和、计数与所有值增量同时为零的组视为全零组，不发送。
// 撤回是否指向现存行由全局阶段在合并时判定（ErrWithdrawBeforeAdd），
// 这样同一条串行无论怎样切批，部分聚合都是可交换、可复现的。
func LocalAggregate(rows []Row) (*Partial, error) {
	log.Printf("[agg-local] 收到一批输入，共 %d 行: %s", len(rows), formatRows(rows))

	type acc struct {
		sumDelta   int64
		countDelta int64
		valueD     map[string]int64
	}

	order := make([]string, 0)
	groups := make(map[string]*acc)

	for i, row := range rows {
		if row.Op != OpAdd && row.Op != OpWithdraw {
			log.Printf("[agg-local] 判定拒绝: 第 %d 行操作非法 %q，依据=operation_not_in{add,withdraw}", i, row.Op)
			return nil, ErrInvalidOperation
		}
		if strings.TrimSpace(row.Group) == "" {
			log.Printf("[agg-local] 判定拒绝: 第 %d 行组名为空，依据=group_name_empty", i)
			return nil, ErrEmptyGroup
		}

		g, ok := groups[row.Group]
		if !ok {
			g = &acc{
				valueD: make(map[string]int64),
			}
			groups[row.Group] = g
			order = append(order, row.Group)
		}

		sign := int64(1)
		if row.Op == OpWithdraw {
			sign = -1
		}
		g.sumDelta += sign * row.Amount
		g.countDelta += sign
		g.valueD[row.Value] += sign
	}

	partial := &Partial{Groups: make([]PartialGroup, 0, len(order))}
	for _, name := range order {
		g := groups[name]
		zeroValues := true
		for _, d := range g.valueD {
			if d != 0 {
				zeroValues = false
				break
			}
		}
		if g.sumDelta == 0 && g.countDelta == 0 && zeroValues {
			log.Printf("[agg-local] 组 %q 判定为全零组（和=0 计数=0 全部值净增减=0），不发送", name)
			continue
		}
		deltas := make(map[string]int64, len(g.valueD))
		for value, d := range g.valueD {
			if d != 0 {
				deltas[value] = d
			}
		}
		partial.Groups = append(partial.Groups, PartialGroup{
			Group:       name,
			SumDelta:    g.sumDelta,
			CountDelta:  g.countDelta,
			ValueDeltas: deltas,
		})
	}

	log.Printf("[agg-local] 判定接受: 产出部分聚合 %s，依据=全部行通过校验且已剔除全零组", formatPartial(partial))
	return partial, nil
}
