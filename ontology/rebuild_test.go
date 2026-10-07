package ontology

import (
	"errors"
	"reflect"
	"testing"
)

// 在 A->B->C 层级上穷举所有长度 <= maxLen 的合法演变路径
// （每步可细化到直接子类型或退回到直接父类型），
// 逐前缀对比 Rebuilder 与朴素重放模型。
func TestEvolutionDirectionSwitchExhaustive(t *testing.T) {
	const maxLen = 6
	moves := map[string][]string{"A": {"B"}, "B": {"A", "C"}, "C": {"B"}}

	var paths [][]string
	var walk func(cur string, path []string)
	walk = func(cur string, path []string) {
		if len(path) > 0 {
			paths = append(paths, append([]string(nil), path...))
		}
		if len(path) == maxLen {
			return
		}
		for _, next := range moves[cur] {
			walk(next, append(path, next))
		}
	}
	walk("A", nil)
	if len(paths) == 0 {
		t.Fatal("no paths generated")
	}
	t.Logf("exhaustive evolution paths: %d", len(paths))

	for _, path := range paths {
		schema := testSchema()
		store := NewEventStore()
		rb := NewRebuilder(store, schema, 3)
		const id = "inst"
		store.Append(create(id, 1, "A"))
		// 在 A 上赋一个会被 B/C 的覆盖范围遮蔽的值。
		store.Append(set(id, 2, "score", IntVal(50)))
		store.Append(set(id, 3, "bOnly", StrVal("x")))
		for i, target := range path {
			store.Append(evolve(id, int64(10+i), target))
		}
		for prefix := 0; prefix <= len(path); prefix++ {
			cutoff := int64(9 + prefix)
			if prefix == 0 {
				cutoff = 3
			}
			got, _, gerr := rb.Rebuild(id, cutoff)
			want, werr := NaiveRebuild(schema, store.Events(id), cutoff)
			if !errors.Is(gerr, werr) {
				t.Fatalf("path %v prefix %d: err %v vs %v", path, prefix, gerr, werr)
			}
			if gerr == nil && !reflect.DeepEqual(got, want) {
				t.Fatalf("path %v prefix %d:\n got %+v\nwant %+v", path, prefix, got, want)
			}
		}
	}
}

// 子类型 -> 父类型回退时的遮蔽语义：
// 父类型下赋的值在子类型覆盖范围外时被遮蔽，退回父类型后恢复可见；
// 子类型新增属性在父类型下无意义，被遮蔽。
func TestMaskingOnGeneralizeAndRestore(t *testing.T) {
	schema := testSchema()
	store := NewEventStore()
	rb := NewRebuilder(store, schema, 2)
	const id = "inst"
	store.Append(create(id, 1, "A"))
	store.Append(set(id, 2, "score", IntVal(50)))  // A:[0,100] 合法
	store.Append(evolve(id, 3, "B"))               // B 覆盖 score->[0,10]
	store.Append(set(id, 4, "bOnly", StrVal("x"))) // B 新增属性

	st, _, err := rb.Rebuild(id, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Values["score"]; ok {
		t.Fatalf("score=50 should be masked under B, got %v", st.Values)
	}
	if st.Suppressed["score"] != IntVal(50) {
		t.Fatalf("score should be suppressed with original value, got %v", st.Suppressed)
	}
	if st.Values["bOnly"] != StrVal("x") {
		t.Fatalf("bOnly should be visible under B, got %v", st.Values)
	}

	store.Append(evolve(id, 5, "A")) // 退回父类型
	st, _, err = rb.Rebuild(id, 5)
	if err != nil {
		t.Fatal(err)
	}
	if st.Values["score"] != IntVal(50) {
		t.Fatalf("score should be visible again under A, got %v", st.Values)
	}
	if st.Suppressed["bOnly"] != StrVal("x") {
		t.Fatalf("bOnly should be masked under A, got %v", st.Suppressed)
	}
}

// 规则版本跨越事件流：事件按发生时刻生效的版本解释；
// 恰好落在切换时点的事件归属新版本；历史时刻的重建不受后续版本影响。
func TestRuleVersionCrossingEventStream(t *testing.T) {
	schema := testSchema()
	// v1 自 t=100 起：A.score 收紧为 [0,60]，并新增 A 的子类型 D。
	v1 := RuleVersion{
		EffectiveFrom: 100,
		Types: map[string]TypeDef{
			"A": {ID: "A", LocalProps: map[string]ValueRange{
				"score": {Kind: IntRangeKind, Min: 0, Max: 60},
			}},
			"D": {ID: "D", Parent: "A"},
		},
	}
	if err := schema.AppendVersion(v1); err != nil {
		t.Fatal(err)
	}
	store := NewEventStore()
	rb := NewRebuilder(store, schema, 2)
	const id = "inst"
	store.Append(create(id, 10, "A"))
	store.Append(set(id, 50, "score", IntVal(80)))  // v0 下合法
	store.Append(set(id, 100, "score", IntVal(70))) // 切换时点：归 v1，[0,60] 拒绝
	store.Append(evolve(id, 110, "D"))              // v1 下 D 已定义

	st, _, err := rb.Rebuild(id, 110)
	if err != nil {
		t.Fatal(err)
	}
	// t=100 的赋值在 v1 下被拒绝，故最终取值仍是 t=50 赋的 80；
	// 但截止时刻 t=110 生效的是 v1（score 范围 [0,60]），
	// 按遮蔽规则 80 越界，应被移入 Suppressed 且保留原值。
	if _, ok := st.Values["score"]; ok {
		t.Fatalf("score=80 should be masked under v1 range [0,60]: %v", st.Values)
	}
	if st.Suppressed["score"] != IntVal(80) {
		t.Fatalf("t=100 assignment of 70 must be rejected under v1; suppressed should hold 80: %v", st.Suppressed)
	}
	if st.TypeID != "D" {
		t.Fatalf("evolution to D at t=110 should succeed under v1, got %v", st.TypeID)
	}

	// 历史时刻重建仍按当时版本解释：t=50 生效 v0，80 合法可见。
	st, _, err = rb.Rebuild(id, 50)
	if err != nil {
		t.Fatal(err)
	}
	if st.Values["score"] != IntVal(80) || st.TypeID != "A" {
		t.Fatalf("historical rebuild at t=50 changed by later version: %+v", st)
	}

	// t=60 时 D 尚未定义：演变到 D 必须报 ErrUndefinedTargetType。
	store.Append(evolve(id, 60, "D"))
	if _, _, err = rb.Rebuild(id, 60); !errors.Is(err, ErrUndefinedTargetType) {
		t.Fatalf("evolve to D at t=60 should be undefined-target, got %v", err)
	}
}

// 四类错误的优先级：同时满足多类条件时只报告优先级最高的一类。
func TestErrorPriority(t *testing.T) {
	newStream := func() (*EventStore, *Rebuilder) {
		schema := testSchema()
		store := NewEventStore()
		return store, NewRebuilder(store, schema, 4)
	}
	const id = "inst"

	t.Run("cutoff beats ambiguity", func(t *testing.T) {
		store, rb := newStream()
		store.Append(create(id, 10, "A"))
		store.Append(set(id, 20, "score", IntVal(1)))
		store.Append(set(id, 20, "score", IntVal(2))) // 并列记录
		_, _, err := rb.Rebuild(id, 5)                // 早于首个事件
		if !errors.Is(err, ErrCutoffBeforeFirstEvent) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("ambiguity beats undefined target", func(t *testing.T) {
		store, rb := newStream()
		store.Append(create(id, 10, "A"))
		store.Append(evolve(id, 20, "Ghost")) // 未定义目标
		store.Append(set(id, 20, "score", IntVal(1)))
		_, _, err := rb.Rebuild(id, 100)
		if !errors.Is(err, ErrAmbiguousOrder) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("undefined target beats invalid path", func(t *testing.T) {
		store, rb := newStream()
		store.Append(create(id, 10, "A"))
		store.Append(evolve(id, 20, "Ghost")) // 未定义目标（先发生）
		store.Append(evolve(id, 30, "C"))     // 非法路径（A->C 跨级）
		_, _, err := rb.Rebuild(id, 100)
		if !errors.Is(err, ErrUndefinedTargetType) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("invalid path reported", func(t *testing.T) {
		store, rb := newStream()
		store.Append(create(id, 10, "A"))
		store.Append(evolve(id, 20, "C")) // A->C 跨级
		_, _, err := rb.Rebuild(id, 100)
		if !errors.Is(err, ErrInvalidEvolutionPath) {
			t.Fatalf("got %v", err)
		}
	})
}

// 重建失败必须保持只读：事件流不发生任何可观察改动。
func TestFailedRebuildIsReadOnly(t *testing.T) {
	schema := testSchema()
	store := NewEventStore()
	rb := NewRebuilder(store, schema, 2)
	const id = "inst"
	store.Append(create(id, 1, "A"))
	store.Append(evolve(id, 2, "Ghost"))
	before := store.Events(id)
	beforeLen := store.Len()

	if _, _, err := rb.Rebuild(id, 100); !errors.Is(err, ErrUndefinedTargetType) {
		t.Fatalf("expected undefined target, got %v", err)
	}
	if store.Len() != beforeLen {
		t.Fatalf("store length changed: %d -> %d", beforeLen, store.Len())
	}
	if after := store.Events(id); !reflect.DeepEqual(before, after) {
		t.Fatalf("event stream mutated by failed rebuild")
	}
}

// 重建开销不得随类型演变事件总数线性增长：
// 通过 RebuildStats.EventsScanned 独立验证——热路径重建扫描的
// 事件数被检查点间隔常数所限，与历史演变事件总数无关。
func TestRebuildCostIndependentOfEvolutionCount(t *testing.T) {
	schema := testSchema()
	store := NewEventStore()
	const every = 64
	rb := NewRebuilder(store, schema, every)
	const id = "inst"

	clock := int64(0)
	store.Append(create(id, 0, "A"))
	appendEvolutions := func(n int) {
		cur := "A"
		for i := 0; i < n; i++ {
			clock++
			next := "B"
			if cur == "B" {
				next = "A"
			}
			store.Append(evolve(id, clock, next))
			cur = next
		}
	}

	appendEvolutions(4000)

	// 冷重建：建立检查点。
	if _, _, err := rb.Rebuild(id, clock); err != nil {
		t.Fatal(err)
	}
	// 热重建：扫描数应 <= every。
	_, stats1, err := rb.Rebuild(id, clock)
	if err != nil {
		t.Fatal(err)
	}
	if !stats1.CheckpointUsed {
		t.Fatal("warm rebuild should use a checkpoint")
	}
	if stats1.EventsScanned > every {
		t.Fatalf("warm rebuild scanned %d events, want <= %d", stats1.EventsScanned, every)
	}

	// 历史演变事件总数翻倍后，先做一次追平重建（只扫描新追加部分），
	// 此后的热重建扫描数与历史总量无关。
	appendEvolutions(4000)
	if _, _, err := rb.Rebuild(id, clock); err != nil { // 追平，建立新检查点
		t.Fatal(err)
	}
	_, stats2, err := rb.Rebuild(id, clock)
	if err != nil {
		t.Fatal(err)
	}
	if stats2.EventsScanned > every {
		t.Fatalf("after doubling history, scanned %d events, want <= %d", stats2.EventsScanned, every)
	}
	t.Logf("scanned: N=4000 -> %d, N=8000 -> %d (checkpoint interval %d)",
		stats1.EventsScanned, stats2.EventsScanned, every)
}
