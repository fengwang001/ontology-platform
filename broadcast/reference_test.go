package broadcast

import (
	"sort"
	"testing"
)

type scriptOp struct {
	kind     string // "publish" | "deliver" | "send"
	changes  []Change
	instance int
	key      int
	value    int
}

// naiveReference 是朴素参照：保存每个版本的完整规则快照；数据结果只由其
// 标签版本决定；每个实例上按数据到达顺序、每条数据内按规则标识排序输出。
func naiveReference(t *testing.T, instances, capacity int, script []scriptOp) [][]Hit {
	t.Helper()
	snapshots := []map[string]Rule{{}}

	type refDatum struct {
		key, value, tag, seq int
	}
	type refInstance struct {
		applied int
		buffer  []refDatum
		hits    []Hit
	}
	states := make([]refInstance, instances)
	global := 0
	seq := 0

	evaluate := func(st *refInstance, inst int, d refDatum) {
		rules := snapshots[d.tag]
		ids := make([]string, 0, len(rules))
		for id := range rules {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if r := rules[id]; d.value >= r.Threshold {
				st.hits = append(st.hits, Hit{
					Instance: inst, Key: d.key, Value: d.value,
					Version: d.tag, RuleID: id, Seq: int64(d.seq),
				})
			}
		}
	}

	for _, op := range script {
		switch op.kind {
		case "publish":
			next := map[string]Rule{}
			for id, r := range snapshots[global] {
				next[id] = r
			}
			for _, c := range op.changes {
				if c.Op == OpUpsert {
					next[c.Rule.ID] = c.Rule
				} else {
					delete(next, c.Rule.ID)
				}
			}
			snapshots = append(snapshots, next)
			global++
		case "deliver":
			st := &states[op.instance]
			if st.applied >= global {
				t.Fatalf("reference: unexpected deliver out of range")
			}
			st.applied++
			kept := st.buffer[:0]
			for _, d := range st.buffer {
				if d.tag == st.applied {
					evaluate(st, op.instance, d)
				} else {
					kept = append(kept, d)
				}
			}
			st.buffer = kept
		case "send":
			if op.key < 0 {
				t.Fatalf("reference: unexpected negative key")
			}
			inst := op.key % instances
			st := &states[inst]
			tag := global
			if tag != st.applied && len(st.buffer) >= capacity {
				t.Fatalf("reference: unexpected buffer full")
			}
			seq++
			d := refDatum{key: op.key, value: op.value, tag: tag, seq: seq}
			if tag == st.applied {
				evaluate(st, inst, d)
			} else {
				st.buffer = append(st.buffer, d)
			}
		}
	}
	out := make([][]Hit, instances)
	for i := range states {
		out[i] = states[i].hits
	}
	return out
}

func sameHits(a, b [][]Hit) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

// TestAgainstNaiveReference：固定同一串发布与发送序列，改变投递穿插方式，
// 引擎最终命中集合必须相同、同一实例上顺序必须相同，且与朴素参照一致。
func TestAgainstNaiveReference(t *testing.T) {
	const instances, capacity = 3, 64
	common := []scriptOp{
		{kind: "publish", changes: []Change{
			{Op: OpUpsert, Rule: Rule{ID: "r1", Threshold: 3}},
			{Op: OpUpsert, Rule: Rule{ID: "r2", Threshold: 7}},
		}},
		{kind: "send", key: 0, value: 5},
		{kind: "send", key: 1, value: 8},
		{kind: "send", key: 3, value: 1},
		{kind: "publish", changes: []Change{
			{Op: OpDelete, Rule: Rule{ID: "r2"}},
			{Op: OpUpsert, Rule: Rule{ID: "r3", Threshold: 2}},
		}},
		{kind: "send", key: 6, value: 2},
		{kind: "send", key: 4, value: 9},
		{kind: "send", key: 2, value: 0},
		{kind: "publish", changes: []Change{
			{Op: OpUpsert, Rule: Rule{ID: "r1", Threshold: 6}},
		}},
		{kind: "send", key: 9, value: 6},
		{kind: "send", key: 7, value: 2},
	}

	// 调度 A：每步之后立刻让全部实例追平到全局版本。
	scheduleA := func(e *Engine) [][]Hit {
		var refScript []scriptOp
		for _, op := range common {
			if op.kind == "publish" {
				mustPublish(t, e, op.changes)
			} else {
				if _, _, err := e.Send(op.key, op.value); err != nil {
					t.Fatalf("scheduleA send: %v", err)
				}
			}
			refScript = append(refScript, op)
			gv := e.GlobalVersion()
			for i := 0; i < instances; i++ {
				for {
					av, _ := e.AppliedVersion(i)
					if av >= gv {
						break
					}
					if _, _, err := e.Deliver(i); err != nil {
						t.Fatalf("scheduleA deliver: %v", err)
					}
					refScript = append(refScript, scriptOp{kind: "deliver", instance: i})
				}
			}
		}
		return e.AllHits()
	}

	// 调度 B：先发布并发送完全部数据，再按实例补齐所有版本。
	scheduleB := func(e *Engine) [][]Hit {
		var refScript []scriptOp
		for _, op := range common {
			refScript = append(refScript, op)
			if op.kind == "publish" {
				mustPublish(t, e, op.changes)
			} else if _, _, err := e.Send(op.key, op.value); err != nil {
				t.Fatalf("scheduleB send: %v", err)
			}
		}
		gv := e.GlobalVersion()
		for i := 0; i < instances; i++ {
			for {
				av, _ := e.AppliedVersion(i)
				if av >= gv {
					break
				}
				if _, _, err := e.Deliver(i); err != nil {
					t.Fatalf("scheduleB deliver: %v", err)
				}
				refScript = append(refScript, scriptOp{kind: "deliver", instance: i})
			}
		}
		_ = refScript
		return e.AllHits()
	}

	// 调度 C：交错延迟投递——实例轮流只投递一个版本。
	scheduleC := func(e *Engine) [][]Hit {
		for _, op := range common {
			if op.kind == "publish" {
				mustPublish(t, e, op.changes)
			} else if _, _, err := e.Send(op.key, op.value); err != nil {
				t.Fatalf("scheduleC send: %v", err)
			}
		}
		gv := e.GlobalVersion()
		for v := 1; v <= gv; v++ {
			for i := 0; i < instances; i++ {
				if av, _ := e.AppliedVersion(i); av < v {
					if _, _, err := e.Deliver(i); err != nil {
						t.Fatalf("scheduleC deliver: %v", err)
					}
				}
			}
		}
		return e.AllHits()
	}

	eA, _ := newTestEngine(t, instances, capacity)
	hitsA := scheduleA(eA)
	eB, _ := newTestEngine(t, instances, capacity)
	hitsB := scheduleB(eB)
	eC, _ := newTestEngine(t, instances, capacity)
	hitsC := scheduleC(eC)

	if !sameHits(hitsA, hitsB) {
		t.Fatalf("schedule A vs B differ:\nA=%v\nB=%v", hitsA, hitsB)
	}
	if !sameHits(hitsA, hitsC) {
		t.Fatalf("schedule A vs C differ:\nA=%v\nC=%v", hitsA, hitsC)
	}

	// 朴素参照：标签由到达时的全局版本决定，与投递无关，因此直接用
	// “全部发送后统一投递”的脚本计算期望结果。
	var refScript []scriptOp
	for _, op := range common {
		refScript = append(refScript, op)
	}
	for i := 0; i < instances; i++ {
		for v := 1; v <= 3; v++ {
			refScript = append(refScript, scriptOp{kind: "deliver", instance: i})
		}
	}
	ref := naiveReference(t, instances, capacity, refScript)
	if !sameHits(hitsA, ref) {
		t.Fatalf("engine vs naive reference differ:\nengine=%v\nref   =%v", hitsA, ref)
	}
}
