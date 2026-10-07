package ontologyindex

import (
	"fmt"
	"math/rand"
	"testing"
)

// engineSnapshot 取出索引当前物化的 value->[]objects 视图用于对照。
func engineSnapshot(t *testing.T, eng *Engine, idx string) map[string][]string {
	t.Helper()
	eng.mu.RLock()
	st := eng.indexes[idx]
	if st == nil {
		eng.mu.RUnlock()
		t.Fatalf("索引 %s 不存在", idx)
	}
	out := map[string][]string{}
	for v, set := range st.active.valueToObjects {
		out[v.str] = append([]string(nil), set.s...)
	}
	eng.mu.RUnlock()
	return out
}

func naiveSnapshot(t *testing.T, m *NaiveModel, idx string, switched bool, cut LogicalClock) map[string][]string {
	t.Helper()
	got, err := m.Rebuild(idx, switched, cut)
	if switched {
		// 唯一约束失败时差分主测试不使用 unique 索引，这里允许忽略 E2。
		if err != nil {
			if ie, ok := err.(*IndexError); ok && ie.Code == ErrSwitchValidation {
				return nil
			}
		}
	}
	out := map[string][]string{}
	for v, ids := range got {
		out[v.str] = ids
	}
	return out
}

func assertSameView(t *testing.T, label string, a, b map[string][]string) {
	t.Helper()
	all := map[string]struct{}{}
	for k := range a {
		all[k] = struct{}{}
	}
	for k := range b {
		all[k] = struct{}{}
	}
	for k := range all {
		aa, bb := a[k], b[k]
		if len(aa) != len(bb) {
			t.Fatalf("[%s] 取值 %q 对象数不一致 engine=%v naive=%v", label, k, aa, bb)
		}
		for i := range aa {
			if aa[i] != bb[i] {
				t.Fatalf("[%s] 取值 %q 内容不一致 engine=%v naive=%v", label, k, aa, bb)
			}
		}
	}
}

// TestRandomDifferentialNoSwitch：纯增量阶段，随机乱序 + 重复事件流，
// 引擎物化视图逐条对照朴素批量重建。
func TestRandomDifferentialNoSwitch(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		s := schemaForTests()
		aud := &MemoryAuditor{}
		eng := NewEngine(s, aud)
		naive := NewNaiveModel(s)
		eng.CreateIndex("idx", "Person", "prop_ssn", ConstraintDuplicate)
		naive.CreateIndex("idx", "Person", "prop_ssn", ConstraintDuplicate)

		n := 60
		var stream []ChangeEvent
		for i := 0; i < n; i++ {
			obj := fmt.Sprintf("p%d", rng.Intn(8))
			ev := ChangeEvent{
				EventID:     fmt.Sprintf("e-%d-%d", seed, i),
				ObjectType:  "Person",
				ObjectID:    obj,
				PropertyID:  "prop_ssn",
				NewValue:    StringValue(fmt.Sprintf("v%d", rng.Intn(5))),
				EffectiveAt: LogicalClock(1 + rng.Intn(98)),
			}
			stream = append(stream, ev)
			if rng.Intn(3) == 0 {
				stream = append(stream, ev) // 随机重复
			}
		}
		rng.Shuffle(len(stream), func(i, j int) { stream[i], stream[j] = stream[j], stream[i] })

		for i, ev := range stream {
			ev.ArrivedAt = int64(i + 1)
			e1 := eng.Ingest(ev)
			e2 := naive.Ingest(ev)
			if (e1 == nil) != (e2 == nil) {
				t.Fatalf("seed=%d event=%s 双方错误不一致 engine=%v naive=%v", seed, ev.EventID, e1, e2)
			}
		}
		assertSameView(t, fmt.Sprintf("seed=%d", seed),
			engineSnapshot(t, eng, "idx"),
			naiveSnapshot(t, naive, "idx", false, 0))

		// 审计：每次 ingest 都必须记录输入、依据版本与结论。
		var ingestRecs int
		for _, r := range aud.Snapshot() {
			if r.Op == "ingest" {
				ingestRecs++
				if r.IndexVersion != 1 || r.Decision == "" {
					t.Fatalf("审计记录缺少版本/结论: %+v", r)
				}
			}
		}
		if ingestRecs != len(stream) {
			t.Fatalf("seed=%d 审计 ingest 记录数 %d != 投递数 %d", seed, ingestRecs, len(stream))
		}
	}
}

// TestRandomDifferentialWithSwitch：随机事件流 + 一次成功切换，
// 切换后新版本视图对照朴素模型 switched=true 的重建结果。
func TestRandomDifferentialWithSwitch(t *testing.T) {
	for seed := int64(0); seed < 30; seed++ {
		rng := rand.New(rand.NewSource(1000 + seed))
		s := schemaForTests()
		eng := NewEngine(s, nil)
		naive := NewNaiveModel(s)
		eng.CreateIndex("idx", "Person", "prop_ssn", ConstraintDuplicate)
		naive.CreateIndex("idx", "Person", "prop_ssn", ConstraintDuplicate)

		mk := func(i int) ChangeEvent {
			obj := fmt.Sprintf("p%d", rng.Intn(6))
			ts := 1 + rng.Intn(198)
			prop := "prop_ssn"
			if ts >= 100 {
				prop = "prop_tax"
			}
			return ChangeEvent{
				EventID:     fmt.Sprintf("s%d-e%d", seed, i),
				ObjectType:  "Person",
				ObjectID:    obj,
				PropertyID:  prop,
				NewValue:    StringValue(fmt.Sprintf("val%d", rng.Intn(10))),
				EffectiveAt: LogicalClock(ts),
			}
		}

		var stream []ChangeEvent
		for i := 0; i < 80; i++ {
			ev := mk(i)
			stream = append(stream, ev)
			if rng.Intn(4) == 0 {
				stream = append(stream, ev)
			}
		}
		rng.Shuffle(len(stream), func(i, j int) { stream[i], stream[j] = stream[j], stream[i] })

		// 在流中间随机位置宣布并完成切换，制造跨越时序。
		cutPos := rng.Intn(len(stream))
		switched := false
		for i, ev := range stream {
			if i == cutPos {
				if err := eng.BeginSwitch("idx", 100); err != nil {
					t.Fatal(err)
				}
				if err := eng.CommitSwitch("idx"); err != nil {
					t.Fatalf("seed=%d 切换意外失败: %v", seed, err)
				}
				switched = true
			}
			ev.ArrivedAt = int64(i + 1)
			if err := eng.Ingest(ev); err != nil {
				t.Fatalf("seed=%d ingest 失败: %v", seed, err)
			}
			naive.Ingest(ev)
		}
		if !switched {
			eng.BeginSwitch("idx", 100)
			if err := eng.CommitSwitch("idx"); err != nil {
				t.Fatal(err)
			}
		}
		assertSameView(t, fmt.Sprintf("after-switch seed=%d", seed),
			engineSnapshot(t, eng, "idx"),
			naiveSnapshot(t, naive, "idx", true, 100))
	}
}

// TestRandomDifferentialFailedSwitchRollback：随机流 + 唯一索引切换，
// 当朴素模型判定新版本违反唯一约束时，引擎必须 E2 回滚，且旧版本视图与
// 朴素模型 switched=false（全部事件重归入旧版本）一致。
func TestRandomDifferentialFailedSwitchRollback(t *testing.T) {
	s := schemaForTests()
	eng := NewEngine(s, nil)
	naive := NewNaiveModel(s)
	eng.CreateIndex("idx", "Person", "prop_ssn", ConstraintUnique)
	naive.CreateIndex("idx", "Person", "prop_ssn", ConstraintUnique)

	rng := rand.New(rand.NewSource(7))
	feed := func(ev ChangeEvent) {
		t.Helper()
		if err := eng.Ingest(ev); err != nil {
			t.Fatal(err)
		}
		naive.Ingest(ev)
	}
	// 旧版本保持唯一。
	feed(ChangeEvent{EventID: "a", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_ssn", NewValue: StringValue("old1"), EffectiveAt: 10})
	feed(ChangeEvent{EventID: "b", ObjectType: "Person", ObjectID: "p2", PropertyID: "prop_ssn", NewValue: StringValue("old2"), EffectiveAt: 20})
	if err := eng.BeginSwitch("idx", 100); err != nil {
		t.Fatal(err)
	}
	// 新版本必然冲突。
	feed(ChangeEvent{EventID: "c", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_tax", NewValue: StringValue("same"), EffectiveAt: 110})
	feed(ChangeEvent{EventID: "d", ObjectType: "Person", ObjectID: "p2", PropertyID: "prop_tax", NewValue: StringValue("same"), EffectiveAt: LogicalClock(rng.Int63n(10) + 120)})

	err := eng.CommitSwitch("idx")
	expectErrorCode(t, err, ErrSwitchValidation)

	assertSameView(t, "rollback",
		engineSnapshot(t, eng, "idx"),
		naiveSnapshot(t, naive, "idx", false, 0))
}
