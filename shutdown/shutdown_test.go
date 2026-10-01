package shutdown

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

// mustRegister 注册服务并在失败时终止测试。
func mustRegister(t *testing.T, o *Orchestrator, now int64, id string, grace int64, deps ...string) {
	t.Helper()
	t.Logf("输入: Register(now=%d, id=%q, grace=%d, deps=%v)", now, id, grace, deps)
	if err := o.Register(now, id, grace, deps); err != nil {
		t.Fatalf("Register(%q) 失败: %v", id, err)
	}
	t.Logf("输出: Register(%q) 成功", id)
}

// mustStart 开始关停并在失败时终止测试。
func mustStart(t *testing.T, o *Orchestrator, t0 int64) {
	t.Helper()
	t.Logf("输入: Start(T0=%d)", t0)
	if err := o.Start(t0); err != nil {
		t.Fatalf("Start(%d) 失败: %v", t0, err)
	}
	t.Logf("输出: Start(T0=%d) 成功, 事件=%v", t0, o.Events())
}

// mustExit 上报退出并在失败时终止测试。
func mustExit(t *testing.T, o *Orchestrator, now int64, id string) {
	t.Helper()
	t.Logf("输入: ReportExit(now=%d, id=%q)", now, id)
	if err := o.ReportExit(now, id); err != nil {
		t.Fatalf("ReportExit(%q, %d) 失败: %v", id, now, err)
	}
	t.Logf("输出: ReportExit(%q, %d) 成功, 事件=%v", id, now, o.Events())
}

// mustQuery 查询服务状态并在失败时终止测试。
func mustQuery(t *testing.T, o *Orchestrator, now int64, id string) Status {
	t.Helper()
	t.Logf("输入: Query(now=%d, id=%q)", now, id)
	st, err := o.Query(now, id)
	if err != nil {
		t.Fatalf("Query(%q, %d) 失败: %v", id, now, err)
	}
	t.Logf("输出: Query(%q) = %+v", id, st)
	return st
}

// expectStatus 断言服务的终止时刻、停止时刻与停止方式，并打印判定依据。
func expectStatus(t *testing.T, st Status, terminatedAt, stoppedAt int64, method StopMethod, reason string) {
	t.Helper()
	t.Logf("判定依据: %s", reason)
	if !st.Terminated || st.TerminatedAt != terminatedAt {
		t.Fatalf("%s: 终止时刻 = (%v, %d), 期望 (true, %d)", st.ID, st.Terminated, st.TerminatedAt, terminatedAt)
	}
	if !st.Stopped || st.StoppedAt != stoppedAt || st.Method != method {
		t.Fatalf("%s: 停止 = (%v, %d, %v), 期望 (true, %d, %v)", st.ID, st.Stopped, st.StoppedAt, st.Method, stoppedAt, method)
	}
}

// TestDiamondDependencyTakesLatestStop 覆盖菱形依赖：D 的终止时刻取
// 其全部依赖者（B、C）停止时刻的最大值（最晚者）。
//
//	A --> B --+
//	|         |
//	+--> C ---+--> D   （X --> Y 表示 X 依赖 Y，X 先停）
func TestDiamondDependencyTakesLatestStop(t *testing.T) {
	o := New()
	mustRegister(t, o, 0, "D", 1000)
	mustRegister(t, o, 0, "B", 1000, "D")
	mustRegister(t, o, 0, "C", 1000, "D")
	mustRegister(t, o, 0, "A", 1000, "B", "C")
	mustStart(t, o, 100)

	st := mustQuery(t, o, 100, "A")
	if !st.Terminated || st.TerminatedAt != 100 || st.Stopped {
		t.Fatalf("A 应在 T0=100 收到终止信号且未停止, 实际 %+v", st)
	}
	t.Logf("判定依据: A 没有依赖者, 终止时刻=T0=100")

	mustExit(t, o, 120, "A")
	for _, id := range []string{"B", "C"} {
		st = mustQuery(t, o, 120, id)
		if !st.Terminated || st.TerminatedAt != 120 || st.Stopped {
			t.Fatalf("%s 应在唯一依赖者 A 停止时刻 120 收到终止信号, 实际 %+v", id, st)
		}
	}
	t.Logf("判定依据: B、C 的唯一依赖者 A 于 120 停止, 故 B、C 终止时刻=120")

	mustExit(t, o, 150, "B")
	st = mustQuery(t, o, 150, "D")
	if st.Terminated {
		t.Fatalf("D 的依赖者 C 尚未停止, D 不应收到终止信号, 实际 %+v", st)
	}
	t.Logf("判定依据: D 的依赖者为 {B,C}, C 仍存活, D 不得终止")

	mustExit(t, o, 180, "C")
	st = mustQuery(t, o, 180, "D")
	if !st.Terminated || st.TerminatedAt != 180 || st.Stopped {
		t.Fatalf("D 终止时刻应取 max(stopB=150, stopC=180)=180, 实际 %+v", st)
	}
	t.Logf("判定依据: D 的终止时刻=max(依赖者停止时刻)=max(150,180)=180")
}

// TestReportAtGraceDeadlineTreatedAsKilled 覆盖恰在宽限期末尾上报：
// 调用先按时刻结算到期强杀，服务在 终止时刻+g 已被强杀，上报被拒绝。
func TestReportAtGraceDeadlineTreatedAsKilled(t *testing.T) {
	o := New()
	mustRegister(t, o, 0, "S", 50)
	mustStart(t, o, 100)

	t.Logf("输入: ReportExit(now=150, id=\"S\"), 其中 150 = 终止时刻100 + 宽限期50")
	err := o.ReportExit(150, "S")
	t.Logf("输出: ReportExit 返回 %v", err)
	if !errors.Is(err, ErrAlreadyStopped) {
		t.Fatalf("恰在宽限期末尾上报应返回 ErrAlreadyStopped, 实际 %v", err)
	}
	t.Logf("判定依据: 调用先结算到期强杀, S 于 100+50=150 被强杀, 上报视为已停止而拒绝")

	st := mustQuery(t, o, 150, "S")
	expectStatus(t, st, 100, 150, Killed, "S 终止于 100, 宽限期 50, 到期未退出故于 150 被强杀")

	// 边界对照：早 1ms 上报则应被接受为自行退出。
	o2 := New()
	mustRegister(t, o2, 0, "S", 50)
	mustStart(t, o2, 100)
	mustExit(t, o2, 149, "S")
	st = mustQuery(t, o2, 149, "S")
	expectStatus(t, st, 100, 149, Exited, "149 < 100+50, 宽限期内上报, 自行退出生效")
}

// TestTwoLevelKillCascade 覆盖连续两级强杀的时刻级联：
// A 依赖 B、B 依赖 C，三者都不自行退出，一次查询触发全部强杀。
func TestTwoLevelKillCascade(t *testing.T) {
	o := New()
	mustRegister(t, o, 0, "C", 30)
	mustRegister(t, o, 0, "B", 20, "C")
	mustRegister(t, o, 0, "A", 10, "B")
	mustStart(t, o, 1000)

	snap, err := o.Snapshot(9999)
	if err != nil {
		t.Fatalf("Snapshot 失败: %v", err)
	}
	t.Logf("输入: Snapshot(now=9999); 输出: %+v", snap)
	got := make(map[string]Status, len(snap))
	for _, st := range snap {
		got[st.ID] = st
	}
	expectStatus(t, got["A"], 1000, 1010, Killed, "A 无依赖者, 终止于 T0=1000, 宽限 10, 强杀于 1010")
	expectStatus(t, got["B"], 1010, 1030, Killed, "B 的依赖者 A 停止于 1010, 终止于 1010, 宽限 20, 强杀于 1030")
	expectStatus(t, got["C"], 1030, 1060, Killed, "C 的依赖者 B 停止于 1030, 终止于 1030, 宽限 30, 强杀于 1060")

	wantEvents := []Event{
		{Time: 1000, ID: "A", Kind: EventTerminated},
		{Time: 1010, ID: "A", Kind: EventKilled},
		{Time: 1010, ID: "B", Kind: EventTerminated},
		{Time: 1030, ID: "B", Kind: EventKilled},
		{Time: 1030, ID: "C", Kind: EventTerminated},
		{Time: 1060, ID: "C", Kind: EventKilled},
	}
	if ev := o.Events(); !reflect.DeepEqual(ev, wantEvents) {
		t.Fatalf("事件序列 = %v, 期望 %v", ev, wantEvents)
	}
	t.Logf("判定依据: 强杀到期时刻按 1010<1030<1060 依次结算, 每级强杀触发下游终止并产生下一级强杀")
}

// TestSingleCallSettlesMultipleDueKillsInOrder 覆盖一次调用跨过多个到期点：
// 独立服务 X、Y 与链 P->Q 的强杀到期时刻交错，必须按时间先后依次结算。
func TestSingleCallSettlesMultipleDueKillsInOrder(t *testing.T) {
	o := New()
	mustRegister(t, o, 0, "Q", 100)
	mustRegister(t, o, 0, "P", 3, "Q")
	mustRegister(t, o, 0, "X", 5)
	mustRegister(t, o, 0, "Y", 8)
	mustStart(t, o, 0)

	snap, err := o.Snapshot(200)
	if err != nil {
		t.Fatalf("Snapshot 失败: %v", err)
	}
	t.Logf("输入: Snapshot(now=200); 输出: %+v", snap)
	got := make(map[string]Status, len(snap))
	for _, st := range snap {
		got[st.ID] = st
	}
	expectStatus(t, got["P"], 0, 3, Killed, "P 无依赖者, 终止于 0, 宽限 3, 强杀于 3")
	expectStatus(t, got["X"], 0, 5, Killed, "X 无依赖者, 终止于 0, 宽限 5, 强杀于 5")
	expectStatus(t, got["Y"], 0, 8, Killed, "Y 无依赖者, 终止于 0, 宽限 8, 强杀于 8")
	expectStatus(t, got["Q"], 3, 103, Killed, "Q 的依赖者 P 停止于 3, 终止于 3, 宽限 100, 强杀于 103")

	wantEvents := []Event{
		{Time: 0, ID: "P", Kind: EventTerminated},
		{Time: 0, ID: "X", Kind: EventTerminated},
		{Time: 0, ID: "Y", Kind: EventTerminated},
		{Time: 3, ID: "P", Kind: EventKilled},
		{Time: 3, ID: "Q", Kind: EventTerminated},
		{Time: 5, ID: "X", Kind: EventKilled},
		{Time: 8, ID: "Y", Kind: EventKilled},
		{Time: 103, ID: "Q", Kind: EventKilled},
	}
	if ev := o.Events(); !reflect.DeepEqual(ev, wantEvents) {
		t.Fatalf("事件序列 = %v, 期望 %v", ev, wantEvents)
	}
	t.Logf("判定依据: 到期强杀按 (到期时刻, 注册序号) 排序 3<5<8<103 依次结算, 级联终止插入对应时刻")
}

// TestUpstreamExitsDownstreamKilled 覆盖上游先自行退出而下游被强杀：
// A 依赖 B，A 在宽限期内自行退出，B 收到终止信号后超期被强杀。
func TestUpstreamExitsDownstreamKilled(t *testing.T) {
	o := New()
	mustRegister(t, o, 0, "B", 40)
	mustRegister(t, o, 0, "A", 100, "B")
	mustStart(t, o, 0)

	mustExit(t, o, 10, "A")
	st := mustQuery(t, o, 10, "A")
	expectStatus(t, st, 0, 10, Exited, "A 终止于 T0=0, 10<0+100 宽限期内上报, 自行退出")

	st = mustQuery(t, o, 10, "B")
	if !st.Terminated || st.TerminatedAt != 10 || st.Stopped {
		t.Fatalf("B 应在 A 停止时刻 10 收到终止信号且未停止, 实际 %+v", st)
	}
	t.Logf("判定依据: B 的唯一依赖者 A 停止于 10, B 终止时刻=10")

	st = mustQuery(t, o, 1000, "B")
	expectStatus(t, st, 10, 50, Killed, "B 终止于 10, 宽限 40, 未上报退出, 于 10+40=50 被强杀")
}

// TestRegisterValidation 覆盖注册阶段的各类整体拒绝原因。
func TestRegisterValidation(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T) *Orchestrator
		id    string
		grace int64
		deps  []string
		want  error
	}{
		{"宽限期为零", func(t *testing.T) *Orchestrator { return New() }, "A", 0, nil, ErrNonPositiveGrace},
		{"宽限期为负", func(t *testing.T) *Orchestrator { return New() }, "A", -3, nil, ErrNonPositiveGrace},
		{"服务重复", func(t *testing.T) *Orchestrator {
			o := New()
			mustRegister(t, o, 0, "A", 1)
			return o
		}, "A", 1, nil, ErrDuplicateService},
		{"依赖未注册服务", func(t *testing.T) *Orchestrator { return New() }, "A", 1, []string{"ghost"}, ErrUnknownDependency},
		{"自依赖成环", func(t *testing.T) *Orchestrator { return New() }, "A", 1, []string{"A"}, ErrDependencyCycle},
		{"开始关停后再注册", func(t *testing.T) *Orchestrator {
			o := New()
			mustRegister(t, o, 0, "A", 1)
			mustStart(t, o, 10)
			return o
		}, "B", 1, nil, ErrRegisterAfterStart},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := tc.setup(t)
			t.Logf("输入: Register(id=%q, grace=%d, deps=%v)", tc.id, tc.grace, tc.deps)
			err := o.Register(10, tc.id, tc.grace, tc.deps)
			t.Logf("输出: %v", err)
			if !errors.Is(err, tc.want) {
				t.Fatalf("期望 %v, 实际 %v", tc.want, err)
			}
			t.Logf("判定依据: 命中拒绝原因 %v", tc.want)
		})
	}
}

// TestStartTwiceAndTimeRegression 覆盖重复开始关停与时刻回拨。
func TestStartTwiceAndTimeRegression(t *testing.T) {
	o := New()
	mustRegister(t, o, 0, "A", 10)
	mustStart(t, o, 100)

	if err := o.Start(100); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("重复开始应返回 ErrAlreadyStarted, 实际 %v", err)
	}
	t.Logf("判定依据: 重复开始关停整体拒绝")

	if err := o.ReportExit(99, "A"); !errors.Is(err, ErrTimeRegression) {
		t.Fatalf("时刻回拨应返回 ErrTimeRegression, 实际 %v", err)
	}
	if _, err := o.Query(50, "A"); !errors.Is(err, ErrTimeRegression) {
		t.Fatalf("查询时刻回拨应返回 ErrTimeRegression, 实际 %v", err)
	}
	if _, err := o.Snapshot(50); !errors.Is(err, ErrTimeRegression) {
		t.Fatalf("快照时刻回拨应返回 ErrTimeRegression, 实际 %v", err)
	}
	t.Logf("判定依据: 任何调用的时刻不得早于此前见过的任一时刻 (maxSeen=100)")

	// 回拨被拒绝后状态不变：A 仍未停止，正常上报成功。
	mustExit(t, o, 105, "A")
	st := mustQuery(t, o, 105, "A")
	expectStatus(t, st, 100, 105, Exited, "回拨调用不改变状态, A 于 105 自行退出")
}

// TestReportExitErrorOrder 覆盖上报退出的错误优先级：
// 时刻回拨 > 服务不存在 > 已停止 > 尚未收到终止信号。
func TestReportExitErrorOrder(t *testing.T) {
	o := New()
	mustRegister(t, o, 0, "B", 1000)
	mustRegister(t, o, 0, "A", 1000, "B")
	mustStart(t, o, 100)

	cases := []struct {
		name string
		now  int64
		id   string
		want error
	}{
		{"回拨且服务不存在时报回拨", 50, "ghost", ErrTimeRegression},
		{"服务不存在", 120, "ghost", ErrServiceNotFound},
		{"尚未收到终止信号", 120, "B", ErrNotTerminated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("输入: ReportExit(now=%d, id=%q)", tc.now, tc.id)
			err := o.ReportExit(tc.now, tc.id)
			t.Logf("输出: %v", err)
			if !errors.Is(err, tc.want) {
				t.Fatalf("期望 %v, 实际 %v", tc.want, err)
			}
			t.Logf("判定依据: 按 回拨>不存在>已停止>未终止 顺序命中 %v", tc.want)
		})
	}

	// 已停止(自行退出后重复上报)。
	mustExit(t, o, 130, "A")
	if err := o.ReportExit(140, "A"); !errors.Is(err, ErrAlreadyStopped) {
		t.Fatalf("重复上报退出应返回 ErrAlreadyStopped, 实际 %v", err)
	}
	t.Logf("判定依据: A 已于 130 自行退出, 重复上报命中 已停止")

	// 已停止(被强杀后上报)：B 在 A 停止后终止于 130, 宽限 1000, 于 1130 被强杀。
	if err := o.ReportExit(1200, "B"); !errors.Is(err, ErrAlreadyStopped) {
		t.Fatalf("强杀后上报应返回 ErrAlreadyStopped, 实际 %v", err)
	}
	st := mustQuery(t, o, 1200, "B")
	expectStatus(t, st, 130, 1130, Killed, "B 终止于 130, 宽限 1000, 于 1130 被强杀, 上报拒绝")
}

// TestRejectedOpsDoNotChangeState 覆盖被拒绝的操作不改变任何服务状态。
func TestRejectedOpsDoNotChangeState(t *testing.T) {
	o := New()
	mustRegister(t, o, 0, "B", 100)
	mustRegister(t, o, 0, "A", 100, "B")
	mustStart(t, o, 1000)
	before := o.Events()

	_ = o.Register(1000, "A", 1, nil)               // 重复
	_ = o.Register(1000, "C", 1, []string{"ghost"}) // 依赖未注册
	_ = o.Register(1000, "C", 0, nil)               // 宽限期非正
	_ = o.Start(1000)                               // 重复开始
	_ = o.ReportExit(1000, "ghost")                 // 服务不存在
	_ = o.ReportExit(1000, "B")                     // 尚未终止
	if err := o.Register(1000, "ok", 1, nil); !errors.Is(err, ErrRegisterAfterStart) {
		t.Fatalf("开始后注册应拒绝, 实际 %v", err)
	}

	if ev := o.Events(); !reflect.DeepEqual(ev, before) {
		t.Fatalf("被拒绝的调用改变了事件序列: %v -> %v", before, ev)
	}
	st := mustQuery(t, o, 1000, "A")
	if !st.Terminated || st.TerminatedAt != 1000 || st.Stopped {
		t.Fatalf("被拒绝的调用改变了 A 的状态: %+v", st)
	}
	t.Logf("判定依据: 全部非法调用被拒后, 事件序列与 A 的状态保持不变")
}

// TestReplayDeterminism 覆盖相同调用序列重放得到完全相同的时刻表。
func TestReplayDeterminism(t *testing.T) {
	script := func() (*Orchestrator, error) {
		o := New()
		steps := []func() error{
			func() error { return o.Register(0, "D", 70, nil) },
			func() error { return o.Register(0, "C", 50, []string{"D"}) },
			func() error { return o.Register(0, "B", 30, []string{"D"}) },
			func() error { return o.Register(0, "A", 10, []string{"B", "C"}) },
			func() error { return o.Start(500) },
			func() error { return o.ReportExit(505, "A") },
			func() error { return o.ReportExit(520, "C") },
		}
		for i, step := range steps {
			if err := step(); err != nil {
				return nil, fmt.Errorf("第 %d 步: %w", i, err)
			}
		}
		return o, nil
	}
	run := func(t *testing.T) ([]Status, []Event) {
		o, err := script()
		if err != nil {
			t.Fatalf("脚本执行失败: %v", err)
		}
		snap, err := o.Snapshot(100000)
		if err != nil {
			t.Fatalf("Snapshot 失败: %v", err)
		}
		return snap, o.Events()
	}
	snap1, ev1 := run(t)
	snap2, ev2 := run(t)
	t.Logf("输入: 固定调用序列重放两次; 输出1: %+v; 输出2: %+v", snap1, snap2)
	if !reflect.DeepEqual(snap1, snap2) || !reflect.DeepEqual(ev1, ev2) {
		t.Fatalf("重放结果不一致:\n%v\n%v", snap1, snap2)
	}
	t.Logf("判定依据: 编排器无随机性与墙上时钟依赖, 相同调用序列产生相同时刻表")
}

// TestConcurrentCalls 覆盖并发调用：结果等价于某个串行顺序，
// 且不变量成立（终止时刻=依赖者停止时刻最大值, 停止-终止<=宽限期）。
func TestConcurrentCalls(t *testing.T) {
	const n = 50
	o := New()
	deps := make(map[string][]string)
	graces := make(map[string]int64)

	var clock atomic.Int64
	next := func() int64 { return clock.Add(1) - 1 }

	// 依赖必须已注册，故注册按链式顺序串行进行：svc-i 依赖 svc-(i-1)。
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("svc-%03d", i)
		graces[id] = int64(10 + i%7)
		var d []string
		if i > 0 {
			d = []string{fmt.Sprintf("svc-%03d", i-1)}
		}
		deps[id] = d
		if err := o.Register(next(), id, graces[id], d); err != nil {
			t.Fatalf("Register(%q) 失败: %v", id, err)
		}
	}

	t0 := next()
	if err := o.Start(t0); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	// 并发上报一半服务退出，另一半并发查询。
	// 原子时钟只保证票号唯一，不保证取锁顺序，故时刻回拨/尚未终止等
	// 合法拒绝允许出现；最终不变量由下方快照统一校验。
	var wg sync.WaitGroup
	var rejected atomic.Int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("svc-%03d", i)
			var err error
			if i%2 == 0 {
				err = o.ReportExit(next(), id)
			} else {
				_, err = o.Query(next(), id)
			}
			if err != nil {
				rejected.Add(1)
			}
		}(i)
	}
	wg.Wait()
	t.Logf("并发阶段被拒绝的调用数(合法): %d", rejected.Load())

	final := next() + 100000
	snap, err := o.Snapshot(final)
	if err != nil {
		t.Fatalf("Snapshot 失败: %v", err)
	}
	stoppedAt := make(map[string]int64, len(snap))
	dependents := make(map[string][]string)
	for id, ds := range deps {
		for _, d := range ds {
			dependents[d] = append(dependents[d], id)
		}
	}
	for _, st := range snap {
		if !st.Terminated || !st.Stopped {
			t.Fatalf("%s 在足够晚的时刻应已终止并停止, 实际 %+v", st.ID, st)
		}
		if st.StoppedAt-st.TerminatedAt > graces[st.ID] {
			t.Fatalf("%s 停止-终止=%d 超过宽限期 %d", st.ID, st.StoppedAt-st.TerminatedAt, graces[st.ID])
		}
		if st.Method == NotStopped {
			t.Fatalf("%s 已停止但停止方式缺失", st.ID)
		}
		stoppedAt[st.ID] = st.StoppedAt
	}
	for _, st := range snap {
		want := t0
		for _, dep := range dependents[st.ID] {
			if stoppedAt[dep] > want {
				want = stoppedAt[dep]
			}
		}
		if st.TerminatedAt != want {
			t.Fatalf("%s 终止时刻=%d, 期望 max(依赖者停止时刻, T0)=%d", st.ID, st.TerminatedAt, want)
		}
	}
	t.Logf("判定依据: 全部 %d 个服务满足 终止时刻=max(依赖者停止时刻, T0=%d) 且 停止-终止<=宽限期", n, t0)
}
