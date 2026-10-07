package ontology

import (
	"errors"
	"reflect"
	"testing"
)

// 便捷构造：标准规则库 + 空事件存储。
func newFixture(t *testing.T) (*RuleStore, *Store) {
	t.Helper()
	rules := standardRules()
	return rules, NewStore(rules)
}

func mustState(t *testing.T, s *Store, id string, cutoff int64) State {
	t.Helper()
	st, _, err := s.Rebuild(id, cutoff)
	if err != nil {
		t.Fatalf("Rebuild(%q, %d) 意外失败: %v", id, cutoff, err)
	}
	return st
}

// TestEvolveDropAndKeep 验证类型演变时的即时重校验：
// 越出父类型范围或在父类型下失去意义的取值被丢弃，仍合规的取值保留。
func TestEvolveDropAndKeep(t *testing.T) {
	_, s := newFixture(t)
	mustAppend(s, "v1", Created(100, "Sedan"))
	mustAppend(s, "v1", Set(110, "wheels", Int(4)))     // Sedan [4,4] 合规
	mustAppend(s, "v1", Set(120, "color", Text("red"))) // 继承自 Vehicle
	mustAppend(s, "v1", Set(130, "sport", Text("yes"))) // Sedan 新增属性

	st := mustState(t, s, "v1", 135)
	if st.TypeID != "Sedan" || len(st.Props) != 3 {
		t.Fatalf("演变前状态不符: %+v", st)
	}

	// Sedan -> Car：wheels=4 仍在 Car [2,8] 内保留；sport 在 Car 下无定义，丢弃。
	mustAppend(s, "v1", Evolve(140, "Car"))
	st = mustState(t, s, "v1", 150)
	want := State{TypeID: "Car", Props: map[string]Value{"wheels": Int(4), "color": Text("red")}}
	if !reflect.DeepEqual(st, want) {
		t.Fatalf("Sedan->Car 后状态不符:\n got %+v\nwant %+v", st, want)
	}

	// Car 下把 wheels 改为 2（Car 合规、Sedan 不合规），再演变回 Sedan 应被丢弃。
	mustAppend(s, "v1", Set(160, "wheels", Int(2)))
	mustAppend(s, "v1", Evolve(170, "Sedan"))
	st = mustState(t, s, "v1", 180)
	want = State{TypeID: "Sedan", Props: map[string]Value{"color": Text("red")}}
	if !reflect.DeepEqual(st, want) {
		t.Fatalf("Car->Sedan 后状态不符:\n got %+v\nwant %+v", st, want)
	}

	// 历史时刻的状态不受后续演变影响。
	st = mustState(t, s, "v1", 135)
	if st.TypeID != "Sedan" || len(st.Props) != 3 {
		t.Fatalf("历史状态被后续事件污染: %+v", st)
	}
}

// TestNoReinterpretationOfPastAssignments 演变前的赋值按当时生效的定义解释：
// 赋值时合规即生效，之后的演变不会“重新解释”该赋值事件本身，
// 只会由演变事件自身触发一次状态迁移（即时重校验）。
func TestNoReinterpretationOfPastAssignments(t *testing.T) {
	_, s := newFixture(t)
	mustAppend(s, "v1", Created(100, "Car"))
	mustAppend(s, "v1", Set(110, "wheels", Int(6))) // Car [2,8] 合规
	mustAppend(s, "v1", Evolve(120, "Vehicle"))     // Vehicle [0,20]：6 保留
	st := mustState(t, s, "v1", 130)
	if st.Props["wheels"] != Int(6) {
		t.Fatalf("Car->Vehicle 后 wheels 应保留: %+v", st)
	}
}

// TestEvolveErrors 区分目标类型未定义与路径矛盾两类错误。
func TestEvolveErrors(t *testing.T) {
	_, s := newFixture(t)
	mustAppend(s, "v1", Created(100, "Car"))

	if err := s.Append("v1", Evolve(110, "Spaceship")); !errors.Is(err, ErrUndefinedTargetType) {
		t.Fatalf("期望 ErrUndefinedTargetType, 得到 %v", err)
	}
	// Car 与 Truck 同为 Vehicle 的子类型，互不在对方继承链上。
	if err := s.Append("v1", Evolve(110, "Truck")); !errors.Is(err, ErrInvalidEvolutionPath) {
		t.Fatalf("期望 ErrInvalidEvolutionPath, 得到 %v", err)
	}
	// 失败的追加不产生任何可观察改动。
	events, _ := s.Events("v1")
	if len(events) != 1 {
		t.Fatalf("失败追加污染了事件流: %d 条事件", len(events))
	}
	// 跨级演变（Sedan <-> Vehicle）合法。
	mustAppend(s, "v1", Evolve(120, "Vehicle"))
	mustAppend(s, "v1", Evolve(130, "Sedan"))
	if st := mustState(t, s, "v1", 140); st.TypeID != "Sedan" {
		t.Fatalf("跨级演变失败: %+v", st)
	}
}

// TestRebuildErrorPriority 多类错误条件同时成立时只报告优先级最高者。
func TestRebuildErrorPriority(t *testing.T) {
	rules := standardRules()
	s := NewStore(rules)

	// 流 A：并列记录 + 未定义目标类型 => 只报 ErrAmbiguousOrder。
	streamA := []Event{
		Created(100, "Car"),
		Evolve(110, "Spaceship"),   // 若被执行会报未定义
		Set(110, "wheels", Int(4)), // 与前一事件时刻并列
	}
	if err := s.ImportEvents("A", streamA); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Rebuild("A", 200); !errors.Is(err, ErrAmbiguousOrder) {
		t.Fatalf("流 A 期望 ErrAmbiguousOrder, 得到 %v", err)
	}

	// 流 B：截止时刻早于首事件 + 后续存在未定义目标 => 只报 ErrCutoffBeforeFirstEvent。
	streamB := []Event{
		Created(100, "Car"),
		Evolve(110, "Spaceship"),
	}
	if err := s.ImportEvents("B", streamB); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Rebuild("B", 50); !errors.Is(err, ErrCutoffBeforeFirstEvent) {
		t.Fatalf("流 B 期望 ErrCutoffBeforeFirstEvent, 得到 %v", err)
	}

	// 流 C：目标类型未定义 + 路径矛盾（同一事件两种解释）=> 只报 ErrUndefinedTargetType。
	streamC := []Event{
		Created(100, "Car"),
		Evolve(110, "Ghost"), // 未定义；若按已定义处理也不在任何链上
	}
	if err := s.ImportEvents("C", streamC); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Rebuild("C", 200); !errors.Is(err, ErrUndefinedTargetType) {
		t.Fatalf("流 C 期望 ErrUndefinedTargetType, 得到 %v", err)
	}

	// 重建失败后事件流保持原样（只读，无补偿事件）。
	for _, tc := range []struct {
		id string
		n  int
	}{{"A", 3}, {"B", 2}, {"C", 2}} {
		events, err := s.Events(tc.id)
		if err != nil || len(events) != tc.n {
			t.Fatalf("实例 %s 事件流被重建改动: n=%d err=%v", tc.id, len(events), err)
		}
	}
}

// TestCutoffBoundary 截止时刻为闭区间上界：等于首事件时刻可重建。
func TestCutoffBoundary(t *testing.T) {
	_, s := newFixture(t)
	mustAppend(s, "v1", Created(100, "Car"))
	mustAppend(s, "v1", Set(110, "wheels", Int(4)))

	if _, _, err := s.Rebuild("v1", 99); !errors.Is(err, ErrCutoffBeforeFirstEvent) {
		t.Fatalf("cutoff=99 期望 ErrCutoffBeforeFirstEvent, 得到 %v", err)
	}
	st := mustState(t, s, "v1", 100)
	if st.TypeID != "Car" || len(st.Props) != 0 {
		t.Fatalf("cutoff=100 状态不符: %+v", st)
	}
	st = mustState(t, s, "v1", 110)
	if st.Props["wheels"] != Int(4) {
		t.Fatalf("cutoff=110 状态不符: %+v", st)
	}
}

// TestAmbiguousOrderOnlyWithinHorizon 并列记录位于截止时刻之后时不影响重建。
func TestAmbiguousOrderOnlyWithinHorizon(t *testing.T) {
	_, s := newFixture(t)
	stream := []Event{
		Created(100, "Car"),
		Set(110, "wheels", Int(4)),
		Set(110, "color", Text("red")), // 与前一事件并列，但位于 cutoff 之后
	}
	if err := s.ImportEvents("v2", stream); err != nil {
		t.Fatal(err)
	}
	st := mustState(t, s, "v2", 105)
	if st.TypeID != "Car" {
		t.Fatalf("cutoff=105 状态不符: %+v", st)
	}
	if _, _, err := s.Rebuild("v2", 110); !errors.Is(err, ErrAmbiguousOrder) {
		t.Fatalf("cutoff=110 期望 ErrAmbiguousOrder, 得到 %v", err)
	}
}

// TestAppendRejectsOutOfRange 追加期严格校验：越界赋值被拒绝且不落盘。
func TestAppendRejectsOutOfRange(t *testing.T) {
	_, s := newFixture(t)
	mustAppend(s, "v1", Created(100, "Sedan"))
	if err := s.Append("v1", Set(110, "wheels", Int(5))); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("期望 ErrOutOfRange, 得到 %v", err)
	}
	if err := s.Append("v1", Set(110, "unknown", Int(1))); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("未定义属性期望 ErrOutOfRange, 得到 %v", err)
	}
	events, _ := s.Events("v1")
	if len(events) != 1 {
		t.Fatalf("失败追加污染了事件流")
	}
}

// TestRebuildTrace 判定记录包含每个事件所依据的规则版本与结论。
func TestRebuildTrace(t *testing.T) {
	_, s := newFixture(t)
	mustAppend(s, "v1", Created(100, "Car"))
	mustAppend(s, "v1", Set(110, "wheels", Int(4)))
	mustAppend(s, "v1", Evolve(120, "Sedan"))

	_, _, trace, err := s.RebuildTrace("v1", 200)
	if err != nil {
		t.Fatal(err)
	}
	if trace.InstanceID != "v1" || trace.Cutoff != 200 {
		t.Fatalf("trace 元数据不符: %+v", trace)
	}
	if len(trace.Entries) != 3 {
		t.Fatalf("trace 条目数不符: %d", len(trace.Entries))
	}
	for i, e := range trace.Entries {
		if e.RuleVersion != 0 {
			t.Fatalf("条目 %d 规则版本不符: %d", i, e.RuleVersion)
		}
		if e.Action == "" {
			t.Fatalf("条目 %d 缺少判定结论", i)
		}
	}
	if trace.Final.TypeID != "Sedan" {
		t.Fatalf("trace 终态不符: %+v", trace.Final)
	}
}
