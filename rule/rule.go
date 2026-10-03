// Package rule 实现生命周期规则的匹配与到期时刻推导。
// 它只读取版本快照并产出整键原子操作计划，不直接修改存储。
package rule

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"ontology/store"
)

// Kind 是规则种类。
type Kind int

const (
	// Expire：当前数据版本到期则追加删除标记。
	Expire Kind = iota
	// NoncurrentExpire：非当前数据版本按保留期到期删除。
	NoncurrentExpire
	// OrphanMarker：键最终只剩一个删除标记时永久删除之。
	OrphanMarker
)

// Action 是结果清单项的动作。
type Action int

const (
	AddMarker Action = iota
	Removed
	Blocked
)

// Rule 是一条生命周期规则。
type Rule struct {
	ID     string
	Prefix []byte
	Kind   Kind
	Days   int
	Keep   int
}

// Item 是结果清单中的一项。
type Item struct {
	Key    []byte
	Ver    int64
	Action Action
}

// Engine 是构造后不可变的规则集合。
type Engine struct {
	rules []Rule
}

// New 构造规则引擎；字段越界或 id 重复为非法参数。
func New(rules []Rule) (*Engine, error) {
	ids := make(map[string]bool, len(rules))
	for _, r := range rules {
		if len(r.ID) == 0 {
			return nil, errors.New("rule: empty id")
		}
		if ids[r.ID] {
			return nil, fmt.Errorf("rule: duplicate id %q", r.ID)
		}
		ids[r.ID] = true
		if r.Kind < Expire || r.Kind > OrphanMarker {
			return nil, fmt.Errorf("rule %q: invalid kind", r.ID)
		}
		if r.Days < 1 || r.Days > 3650 {
			return nil, fmt.Errorf("rule %q: days out of range [1,3650]", r.ID)
		}
		if r.Keep < 0 || r.Keep > 1000 {
			return nil, fmt.Errorf("rule %q: keep out of range [0,1000]", r.ID)
		}
	}
	return &Engine{rules: append([]Rule(nil), rules...)}, nil
}

// Due 返回到期时刻：自 t 起 days 天后向上取整到下一个 UTC 零点，
// 恰在零点则不进位。t 必须非负。
func Due(t int64, days int) int64 { return dueAt(t, days) }

func dueAt(t int64, days int) int64 {
	x := t + int64(days)*86400
	return ((x + 86399) / 86400) * 86400
}

func (e *Engine) match(key []byte, kind Kind) (Rule, bool) {
	var best Rule
	found := false
	for _, r := range e.rules {
		if r.Kind != kind || !bytes.HasPrefix(key, r.Prefix) {
			continue
		}
		if !found ||
			len(r.Prefix) > len(best.Prefix) ||
			(len(r.Prefix) == len(best.Prefix) && r.ID < best.ID) {
			best, found = r, true
		}
	}
	return best, found
}

// Eval 对单个键执行三个阶段（Expire → NoncurrentExpire → OrphanMarker），
// 返回有序结果清单与待应用到存储的原子计划。nextVer 为追加标记将占用的
// 全局版本号（当前最大版本号 + 1）。
func (e *Engine) Eval(key []byte, versions []store.Version, now, nextVer int64) (items []Item, plan store.Plan) {
	work := append([]store.Version(nil), versions...)
	sort.Slice(work, func(i, j int) bool { return work[i].Ver < work[j].Ver })

	// 阶段一：Expire。
	if r, ok := e.match(key, Expire); ok && len(work) > 0 {
		cur := work[len(work)-1]
		if !cur.Marker {
			due := dueAt(cur.C, r.Days)
			if now >= due {
				items = append(items, Item{Key: append([]byte(nil), key...), Ver: nextVer, Action: AddMarker})
				plan.AddMarker = true
				plan.MarkerC = due
				work = append(work, store.Version{Ver: nextVer, C: due, Marker: true, R: 0})
			}
		}
	}

	// 阶段二：NoncurrentExpire。
	if r, ok := e.match(key, NoncurrentExpire); ok && len(work) > 0 {
		type nc struct {
			v   store.Version
			t   int64 // 成为非当前的时刻
			due int64
		}
		var noncurrent []nc
		for i := 0; i+1 < len(work); i++ {
			if work[i].Marker {
				continue // keep 只数非当前数据版本，标记不数
			}
			t := work[i+1].C
			noncurrent = append(noncurrent, nc{v: work[i], t: t, due: dueAt(t, r.Days)})
		}
		// noncurrent 按版本号升序；最新 keep 个（末尾）永不到期。
		protected := r.Keep
		if protected > len(noncurrent) {
			protected = len(noncurrent)
		}
		firstExpiring := len(noncurrent) - protected
		var stage2 []Item
		for idx, n := range noncurrent {
			if idx >= firstExpiring || now < n.due {
				continue
			}
			if n.v.R > now { // r == now 视为无锁
				stage2 = append(stage2, Item{Key: append([]byte(nil), key...), Ver: n.v.Ver, Action: Blocked})
				continue
			}
			stage2 = append(stage2, Item{Key: append([]byte(nil), key...), Ver: n.v.Ver, Action: Removed})
			plan.Remove = append(plan.Remove, n.v.Ver)
		}
		// 同阶段按版本号升序（Blocked 与 Removed 混合排序）。
		sort.Slice(stage2, func(i, j int) bool { return stage2[i].Ver < stage2[j].Ver })
		items = append(items, stage2...)
	}

	// 阶段三：OrphanMarker。基于阶段一、二之后的版本集。
	survivors := make(map[int64]bool, len(work))
	for _, v := range work {
		survivors[v.Ver] = true
	}
	for _, ver := range plan.Remove {
		delete(survivors, ver)
	}
	if _, ok := e.match(key, OrphanMarker); ok && len(survivors) == 1 {
		var only store.Version
		for _, v := range work {
			if survivors[v.Ver] {
				only = v
			}
		}
		if only.Marker {
			items = append(items, Item{Key: append([]byte(nil), key...), Ver: only.Ver, Action: Removed})
			plan.Remove = append(plan.Remove, only.Ver)
		}
	}

	return items, plan
}
