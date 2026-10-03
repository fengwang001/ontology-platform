// Package scan 在规则引擎与存储之上提供带预算与游标的分批扫描。
package scan

import (
	"errors"

	"ontology/rule"
	"ontology/store"
)

// Engine 绑定一个存储与一套规则。
type Engine struct {
	st  *store.Store
	eng *rule.Engine
}

// Outcome 是一次 Scan 的结果。
type Outcome struct {
	Items    []rule.Item
	Cursor   []byte
	Err      error
	examined int64
}

// Examined 返回本次已处理键的代价之和（含因移除失败而撤销的键）。
func (o *Outcome) Examined() int64 { return o.examined }

// New 创建扫描引擎。
func New(st *store.Store, eng *rule.Engine) *Engine { return &Engine{st: st, eng: eng} }

// Scan 从 cursor（空表示最小键）起按键升序处理，budget 为本次代价预算。
func (e *Engine) Scan(now, budget int64, cursor []byte) *Outcome {
	out := &Outcome{}
	if budget < 1 || budget > 1_000_000_000 {
		out.Err = errors.New("scan: budget out of range [1,10^9]")
		out.Cursor = append([]byte(nil), cursor...)
		return out
	}

	keys := e.st.KeysFrom(cursor)
	var used int64
	first := true
	for _, key := range keys {
		keyCopy := append([]byte(nil), key...)
		result, processed, err := store.Modify(e.st, keyCopy, func(versions []store.Version, maxVer int64) (store.Plan, procResult, bool) {
			res := procResult{cost: int64(len(versions))}
			// 本次调用的第一个键无论是否超预算都处理，以保证游标推进。
			if !first && used+res.cost > budget {
				return store.Plan{}, res, true
			}
			res.items, res.plan = e.eng.Eval(keyCopy, versions, now, maxVer+1)
			return res.plan, res, false
		})
		if err != nil {
			out.examined += result.cost
			out.Err = err
			out.Cursor = keyCopy
			return out
		}
		if !processed { // 预算用尽，游标指向本键
			out.Cursor = keyCopy
			return out
		}
		first = false
		used += result.cost
		out.examined += result.cost
		out.Items = append(out.Items, result.items...)
	}

	// 全部处理完：空游标。
	return out
}

type procResult struct {
	items []rule.Item
	plan  store.Plan
	cost  int64
}
