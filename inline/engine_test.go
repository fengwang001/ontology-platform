package inline

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func buildSession(t *testing.T, funcs ...Function) *Session {
	t.Helper()
	reg := NewRegistry()
	for _, f := range funcs {
		if err := reg.Add(f); err != nil {
			t.Fatalf("Add(%s): %v", f.Name, err)
		}
	}
	s := reg.Begin()
	t.Cleanup(s.Close)
	return s
}

func decide(t *testing.T, cfg Config, funcs ...Function) *Report {
	t.Helper()
	rep, err := Decide(buildSession(t, funcs...), cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	t.Logf("输入函数: %+v", funcs)
	t.Logf("预算规则: %+v", cfg)
	t.Logf("决策报告:\n%s", rep)
	return rep
}

func mustFunc(t *testing.T, rep *Report, name string) FunctionReport {
	t.Helper()
	fr, ok := rep.Func(name)
	if !ok {
		t.Fatalf("报告缺少函数 %s", name)
	}
	return fr
}

func decisionByID(t *testing.T, fr FunctionReport, id string) CallDecision {
	t.Helper()
	for _, d := range fr.Decisions {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("函数 %s 的报告中缺少调用点 %s", fr.Name, id)
	return CallDecision{}
}

func decisionIDs(fr FunctionReport) []string {
	ids := make([]string, 0, len(fr.Decisions))
	for _, d := range fr.Decisions {
		ids = append(ids, d.ID)
	}
	return ids
}

// 预算边界：增长预算与全局上限均为硬约束，取等允许，越界一步拒绝。
func TestBudgetBoundary(t *testing.T) {
	mk := func(bSize int64) []Function {
		return []Function{
			{Name: "F", Size: 100, CallSites: []CallSite{
				{Callee: "A", Hotness: 0.9}, {Callee: "B", Hotness: 0.8}, {Callee: "C", Hotness: 0.7}}},
			{Name: "A", Size: 60},
			{Name: "B", Size: bSize},
			{Name: "C", Size: 20},
		}
	}
	t.Run("增长预算取等允许", func(t *testing.T) {
		cfg := Config{CallOverhead: 10, GrowthNum: 1, GrowthDen: 1, MaxSize: 1000, MaxChainRepeat: 3}
		rep := decide(t, cfg, mk(60)...)
		fr := mustFunc(t, rep, "F")
		if fr.FinalSize != 200 {
			t.Fatalf("F 最终尺寸 = %d, 期望 200", fr.FinalSize)
		}
		if d := decisionByID(t, fr, "F#1"); !d.Inlined {
			t.Fatalf("F#1 增长后恰达预算上限, 应内联, 实际: %+v", d)
		}
		if d := decisionByID(t, fr, "F#2"); d.Inlined || d.Reason != RejectBudget {
			t.Fatalf("F#2 应因预算不足拒绝, 实际: %+v", d)
		}
	})
	t.Run("增长预算越界一步拒绝", func(t *testing.T) {
		cfg := Config{CallOverhead: 10, GrowthNum: 1, GrowthDen: 1, MaxSize: 1000, MaxChainRepeat: 3}
		rep := decide(t, cfg, mk(61)...)
		fr := mustFunc(t, rep, "F")
		if d := decisionByID(t, fr, "F#1"); d.Inlined || d.Reason != RejectBudget {
			t.Fatalf("F#1 增长 101 超过预算 100, 应拒绝, 实际: %+v", d)
		}
		if d := decisionByID(t, fr, "F#2"); !d.Inlined {
			t.Fatalf("F#2 应内联, 实际: %+v", d)
		}
		if fr.FinalSize != 160 {
			t.Fatalf("F 最终尺寸 = %d, 期望 160", fr.FinalSize)
		}
	})
	t.Run("绝对上限取等允许", func(t *testing.T) {
		cfg := Config{CallOverhead: 10, GrowthNum: 1, GrowthDen: 1, MaxSize: 200, MaxChainRepeat: 3}
		rep := decide(t, cfg, mk(60)...)
		fr := mustFunc(t, rep, "F")
		if d := decisionByID(t, fr, "F#1"); !d.Inlined {
			t.Fatalf("F#1 尺寸恰达上限 200, 应内联, 实际: %+v", d)
		}
		if d := decisionByID(t, fr, "F#2"); d.Inlined || d.Reason != RejectBudget {
			t.Fatalf("F#2 应因预算不足拒绝, 实际: %+v", d)
		}
		if fr.FinalSize != 200 {
			t.Fatalf("F 最终尺寸 = %d, 期望 200", fr.FinalSize)
		}
	})
	t.Run("绝对上限越界一步拒绝", func(t *testing.T) {
		cfg := Config{CallOverhead: 10, GrowthNum: 1, GrowthDen: 1, MaxSize: 199, MaxChainRepeat: 3}
		rep := decide(t, cfg, mk(60)...)
		fr := mustFunc(t, rep, "F")
		if d := decisionByID(t, fr, "F#1"); d.Inlined || d.Reason != RejectBudget {
			t.Fatalf("F#1 尺寸 200 超过上限 199, 应拒绝, 实际: %+v", d)
		}
		if fr.FinalSize != 160 {
			t.Fatalf("F 最终尺寸 = %d, 期望 160", fr.FinalSize)
		}
	})
}

// 总是内联：突破预算仍内联，超预算计入调用者尺寸并影响后续调用点；
// 但递归规则与结构规则仍然生效。
func TestAlwaysInlineCascade(t *testing.T) {
	cfg := Config{CallOverhead: 10, GrowthNum: 1, GrowthDen: 1, MaxSize: 100000, MaxChainRepeat: 3}
	t.Run("突破预算并连锁影响后续", func(t *testing.T) {
		rep := decide(t, cfg,
			Function{Name: "F", Size: 100, CallSites: []CallSite{
				{Callee: "G", Hotness: 0.9}, {Callee: "H", Hotness: 0.8}}},
			Function{Name: "G", Size: 500, Marks: Marks{AlwaysInline: true}},
			Function{Name: "H", Size: 15},
		)
		fr := mustFunc(t, rep, "F")
		if d := decisionByID(t, fr, "F#0"); !d.Inlined {
			t.Fatalf("G 带总是内联标记, 即使超预算也必须内联, 实际: %+v", d)
		}
		if fr.FinalSize != 590 {
			t.Fatalf("超预算增长须计入尺寸: F 最终尺寸 = %d, 期望 590", fr.FinalSize)
		}
		if d := decisionByID(t, fr, "F#1"); d.Inlined || d.Reason != RejectBudget {
			t.Fatalf("G 超预算后 H 应连锁被拒绝(预算不足), 实际: %+v", d)
		}
	})
	t.Run("总是内联不免除直接递归", func(t *testing.T) {
		rep := decide(t, cfg,
			Function{Name: "R", Size: 10, Marks: Marks{AlwaysInline: true},
				CallSites: []CallSite{{Callee: "R", Hotness: 1}}},
		)
		fr := mustFunc(t, rep, "R")
		if d := decisionByID(t, fr, "R#0"); d.Inlined || d.Reason != RejectDirectRecursion {
			t.Fatalf("直接递归仍须拒绝, 实际: %+v", d)
		}
		if fr.FinalSize != 10 {
			t.Fatalf("R 最终尺寸 = %d, 期望 10", fr.FinalSize)
		}
	})
	t.Run("总是内联不免除结构限制", func(t *testing.T) {
		rep := decide(t, cfg,
			Function{Name: "Q", Size: 10, CallSites: []CallSite{{Callee: "S", Hotness: 1}}},
			Function{Name: "S", Size: 10, Marks: Marks{AlwaysInline: true, NonInlinableStructure: true}},
		)
		fr := mustFunc(t, rep, "Q")
		if d := decisionByID(t, fr, "Q#0"); d.Inlined || d.Reason != RejectNonInlinableStructure {
			t.Fatalf("结构不可内联仍须拒绝, 实际: %+v", d)
		}
	})
}

// 互递归：展开链上限处截止，兄弟分支独立计数；直接递归一律拒绝。
func TestMutualRecursionChainLimit(t *testing.T) {
	cfg := Config{CallOverhead: 0, GrowthNum: 1000, GrowthDen: 1, MaxSize: 100000, MaxChainRepeat: 2}
	rep := decide(t, cfg,
		Function{Name: "F", Size: 10, CallSites: []CallSite{
			{Callee: "A", Hotness: 0.9}, {Callee: "B", Hotness: 0.8}}},
		Function{Name: "A", Size: 10, CallSites: []CallSite{{Callee: "F", Hotness: 1}}},
		Function{Name: "B", Size: 10, CallSites: []CallSite{{Callee: "F", Hotness: 1}}},
	)
	fr := mustFunc(t, rep, "F")
	if fr.FinalSize != 90 {
		t.Fatalf("F 最终尺寸 = %d, 期望 90", fr.FinalSize)
	}
	inlined, chainLimited := 0, 0
	for _, d := range fr.Decisions {
		if d.Inlined {
			inlined++
		} else if d.Reason == RejectChainLimit {
			chainLimited++
		} else {
			t.Fatalf("意外决策: %+v", d)
		}
	}
	if inlined != 8 || chainLimited != 4 {
		t.Fatalf("内联 %d 处/链超限 %d 处, 期望 8/4", inlined, chainLimited)
	}
	// 兄弟分支互不影响: A、B 两个分支各自独立地把 F 内联了一次。
	for _, id := range []string{"F#0/A#0", "F#1/B#0"} {
		if d := decisionByID(t, fr, id); !d.Inlined {
			t.Fatalf("兄弟分支 %s 应各自内联 F, 实际: %+v", id, d)
		}
	}
	if want := []string{"F", "A", "F", "A"}; !reflect.DeepEqual(fr.DeepestPath, want) {
		t.Fatalf("最深展开链 = %v, 期望 %v", fr.DeepestPath, want)
	}
}

func TestDirectRecursionRejected(t *testing.T) {
	cfg := Config{CallOverhead: 0, GrowthNum: 1000, GrowthDen: 1, MaxSize: 100000, MaxChainRepeat: 5}
	rep := decide(t, cfg,
		Function{Name: "D", Size: 10, CallSites: []CallSite{{Callee: "D", Hotness: 1}}},
	)
	fr := mustFunc(t, rep, "D")
	if d := decisionByID(t, fr, "D#0"); d.Inlined || d.Reason != RejectDirectRecursion {
		t.Fatalf("直接递归应拒绝, 实际: %+v", d)
	}
	if fr.FinalSize != 10 {
		t.Fatalf("D 最终尺寸 = %d, 期望 10", fr.FinalSize)
	}
}

// 考察次序：热度降序、并列按位置；新调用点按同一规则插入重排，而非追加末尾。
func TestExaminationOrder(t *testing.T) {
	cfg := Config{CallOverhead: 0, GrowthNum: 1000, GrowthDen: 1, MaxSize: 100000, MaxChainRepeat: 5}
	t.Run("新调用点插入重排", func(t *testing.T) {
		rep := decide(t, cfg,
			Function{Name: "F", Size: 10, CallSites: []CallSite{
				{Callee: "A", Hotness: 0.5}, {Callee: "B", Hotness: 0.5}}},
			Function{Name: "A", Size: 5, CallSites: []CallSite{{Callee: "C", Hotness: 1}}},
			Function{Name: "B", Size: 5},
			Function{Name: "C", Size: 3},
		)
		fr := mustFunc(t, rep, "F")
		want := []string{"F#0", "F#0/A#0", "F#1"}
		if got := decisionIDs(fr); !reflect.DeepEqual(got, want) {
			t.Fatalf("考察次序 = %v, 期望 %v", got, want)
		}
		if fr.FinalSize != 23 {
			t.Fatalf("F 最终尺寸 = %d, 期望 23", fr.FinalSize)
		}
	})
	t.Run("热度缩放后参与排序", func(t *testing.T) {
		rep := decide(t, cfg,
			Function{Name: "F", Size: 10, CallSites: []CallSite{
				{Callee: "A", Hotness: 0.6}, {Callee: "B", Hotness: 0.4}}},
			Function{Name: "A", Size: 5, CallSites: []CallSite{{Callee: "C", Hotness: 1}}},
			Function{Name: "B", Size: 5, CallSites: []CallSite{{Callee: "D", Hotness: 1}}},
			Function{Name: "C", Size: 3},
			Function{Name: "D", Size: 3},
		)
		fr := mustFunc(t, rep, "F")
		want := []string{"F#0", "F#0/A#0", "F#1", "F#1/B#0"}
		if got := decisionIDs(fr); !reflect.DeepEqual(got, want) {
			t.Fatalf("考察次序 = %v, 期望 %v", got, want)
		}
	})
}

// 拒绝原因优先级：被调函数未定义 > 禁止内联标记 > 直接递归 >
// 结构不可内联 > 展开链超限 > 预算不足；只报第一个成立的原因。
func TestRejectReasonPriority(t *testing.T) {
	wide := Config{CallOverhead: 0, GrowthNum: 1000, GrowthDen: 1, MaxSize: 100000, MaxChainRepeat: 5}
	cases := []struct {
		name   string
		cfg    Config
		funcs  []Function
		root   string
		site   string
		reason RejectReason
	}{
		{
			name: "未定义优先于预算",
			cfg:  Config{CallOverhead: 0, GrowthNum: 0, GrowthDen: 1, MaxSize: 100000, MaxChainRepeat: 5},
			funcs: []Function{
				{Name: "F", Size: 10, CallSites: []CallSite{{Callee: "ghost", Hotness: 0.5}}},
			},
			root: "F", site: "F#0", reason: RejectUndefined,
		},
		{
			name: "禁止内联优先于结构",
			cfg:  wide,
			funcs: []Function{
				{Name: "F", Size: 10, CallSites: []CallSite{{Callee: "G", Hotness: 1}}},
				{Name: "G", Size: 10, Marks: Marks{NoInline: true, NonInlinableStructure: true}},
			},
			root: "F", site: "F#0", reason: RejectNoInline,
		},
		{
			name: "禁止内联优先于直接递归",
			cfg:  wide,
			funcs: []Function{
				{Name: "F", Size: 10, Marks: Marks{NoInline: true},
					CallSites: []CallSite{{Callee: "F", Hotness: 1}}},
			},
			root: "F", site: "F#0", reason: RejectNoInline,
		},
		{
			name: "直接递归优先于结构",
			cfg:  wide,
			funcs: []Function{
				{Name: "F", Size: 10, Marks: Marks{NonInlinableStructure: true},
					CallSites: []CallSite{{Callee: "F", Hotness: 1}}},
			},
			root: "F", site: "F#0", reason: RejectDirectRecursion,
		},
		{
			name: "结构优先于展开链超限",
			cfg:  Config{CallOverhead: 0, GrowthNum: 1000, GrowthDen: 1, MaxSize: 100000, MaxChainRepeat: 1},
			funcs: []Function{
				{Name: "G", Size: 10, Marks: Marks{NonInlinableStructure: true},
					CallSites: []CallSite{{Callee: "A", Hotness: 1}}},
				{Name: "A", Size: 10, CallSites: []CallSite{{Callee: "G", Hotness: 1}}},
			},
			root: "G", site: "G#0/A#0", reason: RejectNonInlinableStructure,
		},
		{
			name: "展开链超限优先于预算",
			cfg:  Config{CallOverhead: 0, GrowthNum: 1, GrowthDen: 1, MaxSize: 100000, MaxChainRepeat: 1},
			funcs: []Function{
				{Name: "F", Size: 10, CallSites: []CallSite{{Callee: "A", Hotness: 1}}},
				{Name: "A", Size: 10, CallSites: []CallSite{{Callee: "F", Hotness: 1}}},
			},
			root: "F", site: "F#0/A#0", reason: RejectChainLimit,
		},
		{
			name: "预算不足",
			cfg:  Config{CallOverhead: 0, GrowthNum: 0, GrowthDen: 1, MaxSize: 100000, MaxChainRepeat: 5},
			funcs: []Function{
				{Name: "F", Size: 100, CallSites: []CallSite{{Callee: "G", Hotness: 1}}},
				{Name: "G", Size: 50},
			},
			root: "F", site: "F#0", reason: RejectBudget,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := decide(t, tc.cfg, tc.funcs...)
			fr := mustFunc(t, rep, tc.root)
			d := decisionByID(t, fr, tc.site)
			if d.Inlined || d.Reason != tc.reason {
				t.Fatalf("调用点 %s 应拒绝(%v), 实际: %+v", tc.site, tc.reason, d)
			}
		})
	}
}

// 内联复制的是被调函数当前的函数体：已完成的根给出最终尺寸与幸存
// 调用点，未完成的根给出初始尺寸与原始调用点（按登记顺序处理）。
func TestCrossRootCurrentBody(t *testing.T) {
	cfg := Config{CallOverhead: 10, GrowthNum: 100, GrowthDen: 1, MaxSize: 100000, MaxChainRepeat: 3}
	rep := decide(t, cfg,
		Function{Name: "E", Size: 10, CallSites: []CallSite{{Callee: "F", Hotness: 1}}},
		Function{Name: "F", Size: 100, CallSites: []CallSite{{Callee: "H", Hotness: 1}}},
		Function{Name: "G", Size: 10, CallSites: []CallSite{{Callee: "F", Hotness: 1}}},
		Function{Name: "H", Size: 50},
	)
	fF := mustFunc(t, rep, "F")
	if fF.FinalSize != 140 {
		t.Fatalf("F 最终尺寸 = %d, 期望 140", fF.FinalSize)
	}
	// E 在 F 之前处理: 复制 F 的初始函数体(尺寸 100, 含原始调用点 H)。
	fE := mustFunc(t, rep, "E")
	if fE.FinalSize != 140 {
		t.Fatalf("E 最终尺寸 = %d, 期望 140", fE.FinalSize)
	}
	if got, want := decisionIDs(fE), []string{"E#0", "E#0/F#0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("E 的考察次序 = %v, 期望 %v", got, want)
	}
	// G 在 F 之后处理: 复制 F 的最终函数体(尺寸 140, 无幸存调用点)。
	fG := mustFunc(t, rep, "G")
	if fG.FinalSize != 140 {
		t.Fatalf("G 最终尺寸 = %d, 期望 140", fG.FinalSize)
	}
	if got, want := decisionIDs(fG), []string{"G#0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("G 的考察次序 = %v, 期望 %v", got, want)
	}
}

func TestValidation(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Add(Function{Name: "x", Size: 1, Marks: Marks{NoInline: true, AlwaysInline: true}}); err == nil {
		t.Fatal("禁止内联与总是内联并存应判输入错误")
	}
	if err := reg.Add(Function{Name: "x", Size: -1}); err == nil {
		t.Fatal("负尺寸应判输入错误")
	}
	if err := reg.Add(Function{Name: "x", Size: 1, CallSites: []CallSite{{Callee: "y", Hotness: 1.5}}}); err == nil {
		t.Fatal("热度超出 [0,1] 应判输入错误")
	}
	if err := reg.Add(Function{Name: "x", Size: 1}); err != nil {
		t.Fatalf("合法函数应登记成功: %v", err)
	}
	if err := reg.Add(Function{Name: "x", Size: 2}); !errors.Is(err, ErrDuplicateFunc) {
		t.Fatalf("重名登记应返回 ErrDuplicateFunc, 实际: %v", err)
	}
	sess := buildSession(t, Function{Name: "F", Size: 1})
	if _, err := Decide(sess, Config{CallOverhead: 0, GrowthNum: 1, GrowthDen: 0, MaxSize: 1, MaxChainRepeat: 1}); err == nil {
		t.Fatal("GrowthDen 为 0 应判配置错误")
	}
	if _, err := Decide(sess, Config{CallOverhead: 0, GrowthNum: 1, GrowthDen: 1, MaxSize: 1, MaxChainRepeat: 0}); err == nil {
		t.Fatal("MaxChainRepeat 为 0 应判配置错误")
	}
}

// 相同输入在顺序与并发执行下输出完全相同的决策与尺寸。
func TestDeterminism(t *testing.T) {
	funcs := []Function{
		{Name: "F", Size: 100, CallSites: []CallSite{
			{Callee: "G", Hotness: 0.9}, {Callee: "H", Hotness: 0.8}}},
		{Name: "G", Size: 60, CallSites: []CallSite{{Callee: "F", Hotness: 1}}},
		{Name: "H", Size: 20, CallSites: []CallSite{{Callee: "G", Hotness: 0.5}}},
	}
	cfg := Config{CallOverhead: 5, GrowthNum: 2, GrowthDen: 1, MaxSize: 10000, MaxChainRepeat: 2}
	want := decide(t, cfg, funcs...)
	for i := 0; i < 3; i++ {
		if got := decide(t, cfg, funcs...); !reflect.DeepEqual(got, want) {
			t.Fatal("重复执行结果不一致")
		}
	}
	const n = 8
	got := make([]*Report, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reg := NewRegistry()
			for _, f := range funcs {
				if err := reg.Add(f); err != nil {
					t.Error(err)
					return
				}
			}
			s := reg.Begin()
			defer s.Close()
			r, err := Decide(s, cfg)
			if err != nil {
				t.Error(err)
				return
			}
			got[i] = r
		}(i)
	}
	wg.Wait()
	for i, r := range got {
		if !reflect.DeepEqual(r, want) {
			t.Fatalf("并发执行 %d 结果不一致", i)
		}
	}
}
