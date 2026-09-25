package pipe

import (
	"errors"
	"slices"
	"testing"

	"ontology/pred"
)

// 链工厂：每条返回全新算子（状态过滤器回到初始状态）。
var chainFns = map[string]func() []Op{
	"F1-S-F2": func() []Op { return []Op{Filter(pred.Even()), RecordHigh(), Filter(pred.KindEq("A"))} },
	"F2-F1-S-F1": func() []Op {
		return []Op{Filter(pred.KindEq("A")), Filter(pred.Even()), RecordHigh(), Filter(pred.Even())}
	},
	"F1-S-F2-S-F1": func() []Op {
		return []Op{Filter(pred.Even()), RecordHigh(), Filter(pred.KindEq("A")), RecordHigh(), Filter(pred.Even())}
	},
}

func genSeqs() [][]pred.Event {
	seqs := [][]pred.Event{{
		{Seq: 1, Val: 10, Kind: "A"}, {Seq: 2, Val: 25, Kind: "A"},
		{Seq: 3, Val: 18, Kind: "A"}, {Seq: 4, Val: 30, Kind: "B"},
		{Seq: 5, Val: 22, Kind: "A"}, {Seq: 6, Val: 40, Kind: "A"},
	}}
	for n := 1; n <= 3; n++ {
		var evs []pred.Event
		for i := 1; i <= 30; i++ {
			kind := "A"
			if (i*n)%4 == 0 {
				kind = "B"
			}
			evs = append(evs, pred.Event{Seq: int64(i), Val: int64((i*11 + n*17) % 60), Kind: kind})
		}
		seqs = append(seqs, evs)
	}
	return seqs
}

// TestComplexityLinear 相邻可交换性检查次数不随 m 二次增长（单遍扫描）。
func TestComplexityLinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		ops := make([]Op, m)
		for i := range ops {
			ops[i] = Filter(pred.Even())
		}
		p, err := Build(ops)
		if err != nil {
			t.Fatal(err)
		}
		if p.Len() != 1 {
			t.Fatalf("m=%d: 合并后算子数=%d, 应为 1", m, p.Len())
		}
		if p.checks != m-1 {
			t.Fatalf("m=%d: checks=%d, 应为线性 m-1=%d（全对全比较会是 m(m-1)/2=%d）", m, p.checks, m-1, m*(m-1)/2)
		}
	}
	// 交替链 F,S,F,S,...：n 个算子检查 n-1 次，同样线性。
	ops := []Op{Filter(pred.Even()), RecordHigh(), Filter(pred.KindEq("A")), RecordHigh(), Filter(pred.Even())}
	p, err := Build(ops)
	if err != nil {
		t.Fatal(err)
	}
	if p.checks != len(ops)-1 {
		t.Fatalf("交替链 checks=%d, 应为 %d", p.checks, len(ops)-1)
	}
}

// TestNaiveEquivalence 不变量1：优化链与朴素链逐事件一致。
func TestNaiveEquivalence(t *testing.T) {
	for name, cf := range chainFns {
		for si, evs := range genSeqs() {
			plan, err := Build(cf())
			if err != nil {
				t.Fatal(err)
			}
			if got, want := plan.Run(evs), RunNaive(cf(), evs); !slices.Equal(got, want) {
				t.Errorf("%s seq%d: got %v, want %v", name, si, got, want)
			}
		}
	}
}

// TestSegmentReorderMerge 不变量2：段内重排与 AND 合并后输出不变。
func TestSegmentReorderMerge(t *testing.T) {
	legal := []Move{{From: 0, To: 1}, {From: 1, To: 0}}
	for _, mv := range legal {
		for si, evs := range genSeqs() {
			plan, err := Build(chainFns["F2-F1-S-F1"](), mv)
			if err != nil {
				t.Fatalf("合法重排被拒: %v", err)
			}
			if got, want := plan.Run(evs), RunNaive(chainFns["F2-F1-S-F1"](), evs); !slices.Equal(got, want) {
				t.Errorf("move %+v seq%d: got %v, want %v", mv, si, got, want)
			}
		}
	}
	// 段内无状态各自合并：F2-F1-S-F1 → [AND, S, AND]，计划长度 3。
	plan, err := Build(chainFns["F2-F1-S-F1"]())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Len() != 3 {
		t.Fatalf("合并后算子数=%d, 应为 3", plan.Len())
	}
}

// TestBarrierRejected 不变量3：任何跨越状态过滤器的移动都被拒绝。
func TestBarrierRejected(t *testing.T) {
	tests := []struct {
		name string
		mv   Move
	}{
		{"F2移到S之上", Move{From: 2, To: 0}},
		{"F1移到S之下", Move{From: 0, To: 2}},
		{"移动状态过滤器", Move{From: 1, To: 0}},
		{"状态过滤器后移", Move{From: 1, To: 2}},
		{"From越界", Move{From: 5, To: 0}},
		{"To越界", Move{From: 0, To: 5}},
	}
	for _, tt := range tests {
		plan, err := Build(chainFns["F1-S-F2"](), tt.mv)
		if !errors.Is(err, ErrBarrier) {
			t.Errorf("%s: err=%v, 应为 ErrBarrier", tt.name, err)
		}
		if plan != nil {
			t.Errorf("%s: 被拒请求不得产出计划", tt.name)
		}
	}
}

// TestBadPredicate 非法谓词在构建期被拒。
func TestBadPredicate(t *testing.T) {
	tests := []struct {
		name string
		p    pred.Stateless
	}{
		{"空谓词", pred.Stateless{}},
		{"引用不存在的字段", pred.OfField(99, "")},
		{"空Kind操作数", pred.KindEq("")},
	}
	for _, tt := range tests {
		if _, err := Build([]Op{Filter(tt.p)}); !errors.Is(err, pred.ErrBadPredicate) {
			t.Errorf("%s: err=%v, 应为 ErrBadPredicate", tt.name, err)
		}
	}
}
