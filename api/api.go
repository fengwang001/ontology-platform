// Package api 对外暴露部分列更新事件的批内合并服务。
package api

import (
	"fmt"
	"maps"
	"sync"

	"ontology/cbatch"
	"ontology/pcol"
)

// Table 是并发安全的对外表句柄。
type Table struct {
	mu   sync.RWMutex
	cols []string
	t    *cbatch.Table
}

// New 构造表；列名为空或重复返回错误。
func New(cols []string) (*Table, error) {
	ct, err := cbatch.NewTable(cols)
	if err != nil {
		return nil, err
	}
	return &Table{cols: append([]string(nil), cols...), t: ct}, nil
}

// Apply 校验并合并一批事件，返回合并后的输出并更新表；整批要么全生效要么全不生效。
func (t *Table) Apply(batch []pcol.Event) ([]pcol.Event, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.t.ApplyBatch(batch)
}

// Row 返回一行的副本，可并发调用。
func (t *Table) Row(key string) (map[string]pcol.Value, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.t.Row(key)
}

func naiveApply(rows map[string]map[string]pcol.Value, batch []pcol.Event) {
	for _, e := range batch {
		if e.Kind == pcol.Insert {
			r := make(map[string]pcol.Value, len(e.Set))
			for c, v := range e.Set {
				r[c] = v
			}
			rows[e.Key] = r
			continue
		}
		for c, v := range e.Set {
			rows[e.Key][c] = v
		}
	}
}

// SelfCheck 用内置批在私有表上核验四条不变量，不触碰共享状态，可并发调用。
func (t *Table) SelfCheck() error {
	t.mu.RLock()
	cols := append([]string(nil), t.cols...)
	t.mu.RUnlock()
	ct, err := cbatch.NewTable(cols)
	if err != nil {
		return err
	}
	full := func(s string) map[string]pcol.Value {
		r := make(map[string]pcol.Value, len(cols))
		for _, c := range cols {
			r[c] = pcol.Str(s + c)
		}
		return r
	}
	if _, err := ct.ApplyBatch([]pcol.Event{{Kind: pcol.Insert, Key: "k", Set: full("k")}}); err != nil {
		return fmt.Errorf("selfcheck seed: %w", err)
	}
	pre, _ := ct.Row("k")
	c0 := cols[0]
	batch := []pcol.Event{
		{Kind: pcol.Insert, Key: "j", Set: full("j")},
		{Kind: pcol.Update, Key: "k", Set: map[string]pcol.Value{c0: pcol.Str("x")}, Before: map[string]pcol.Value{c0: pre[c0]}},
		{Kind: pcol.Update, Key: "k", Set: map[string]pcol.Value{c0: pcol.Null()}, Before: map[string]pcol.Value{c0: pcol.Str("x")}},
	}
	out, err := ct.ApplyBatch(batch)
	if err != nil {
		return fmt.Errorf("selfcheck apply: %w", err)
	}
	// 不变量 3：至多一条 + 首现顺序。
	if len(out) != 2 || out[0].Key != "j" || out[1].Key != "k" {
		return fmt.Errorf("selfcheck: invariant 3 violated")
	}
	// 不变量 2：Before 镜像批前值，且无 Set==Before 列。
	ku := out[1]
	if ku.Kind != pcol.Update || !maps.Equal(ku.Before, map[string]pcol.Value{c0: pre[c0]}) {
		return fmt.Errorf("selfcheck: invariant 2 violated")
	}
	for c, v := range ku.Set {
		if v == ku.Before[c] {
			return fmt.Errorf("selfcheck: invariant 2 violated")
		}
	}
	// 不变量 1：合并输出应用到批前状态 == 朴素逐条应用。
	naive := map[string]map[string]pcol.Value{"k": pre}
	naiveApply(naive, batch)
	for key, want := range naive {
		got, ok := ct.Row(key)
		if !ok || !maps.Equal(got, want) {
			return fmt.Errorf("selfcheck: invariant 1 violated")
		}
	}
	// 不变量 4：被拒批不改变状态。
	before, _ := ct.Row("k")
	bad := []pcol.Event{{Kind: pcol.Update, Key: "k", Set: map[string]pcol.Value{c0: pcol.Str("y")}, Before: map[string]pcol.Value{c0: pcol.Str("wrong")}}}
	if _, err := ct.ApplyBatch(bad); err == nil {
		return fmt.Errorf("selfcheck: invariant 4 violated")
	}
	after, _ := ct.Row("k")
	if !maps.Equal(before, after) {
		return fmt.Errorf("selfcheck: invariant 4 violated")
	}
	return nil
}
