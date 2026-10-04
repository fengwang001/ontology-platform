package sched

import (
	"errors"
	"fmt"
	"testing"

	"ontology/group"
)

// ---- 测试用操作表示（表驱动与朴素模拟器共用）----

type opKind int

const (
	opSubmit opKind = iota
	opFinish
	opFinishFail
	opAckCancel
	opCancel
)

type op struct {
	kind   opKind
	group  []byte
	cancel bool
	prot   bool
	id     int
}

func (o op) String() string {
	switch o.kind {
	case opSubmit:
		return fmt.Sprintf("Submit(g=%q,cancel=%v,prot=%v)", o.group, o.cancel, o.prot)
	case opFinish:
		return fmt.Sprintf("Finish(id=%d,ok=true)", o.id)
	case opFinishFail:
		return fmt.Sprintf("Finish(id=%d,ok=false)", o.id)
	case opAckCancel:
		return fmt.Sprintf("AckCancel(id=%d)", o.id)
	default:
		return fmt.Sprintf("Cancel(id=%d)", o.id)
	}
}

func applyOp(s *Scheduler, o op) (int, error) {
	switch o.kind {
	case opSubmit:
		return s.Submit(o.group, o.cancel, o.prot)
	case opFinish:
		return 0, s.Finish(o.id, true)
	case opFinishFail:
		return 0, s.Finish(o.id, false)
	case opAckCancel:
		return 0, s.AckCancel(o.id)
	default:
		return 0, s.Cancel(o.id)
	}
}

func gstr(s string) []byte { return []byte(s) }

func statesOf(s *Scheduler, ids ...int) []group.State {
	out := make([]group.State, len(ids))
	for i, id := range ids {
		st, ok := s.StateOf(id)
		if !ok {
			out[i] = -1
		} else {
			out[i] = st
		}
	}
	return out
}

func sameStates(got []group.State, want ...group.State) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// ---- 题目范例一：顶替、晋升排队尾、Cancelling 上迟到的 Finish ----

func TestExample1(t *testing.T) {
	s, err := New(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	must := func(got int, e error, want int, ctx string) {
		if e != nil {
			t.Fatalf("%s: unexpected error %v", ctx, e)
		}
		if got != want {
			t.Fatalf("%s: got id %d want %d", ctx, got, want)
		}
	}
	r1, e := s.Submit(gstr("g"), false, false)
	must(r1, e, 1, "r1")
	r2, e := s.Submit(gstr("g"), false, false)
	must(r2, e, 2, "r2")
	r3, e := s.Submit(gstr("g"), false, false)
	must(r3, e, 3, "r3")
	r4, e := s.Submit(gstr("g"), true, false)
	must(r4, e, 4, "r4")
	r5, e := s.Submit(nil, false, false)
	must(r5, e, 5, "r5")

	if got := statesOf(s, r1, r2, r3, r4, r5); !sameStates(got,
		group.Cancelling, group.Superseded, group.Superseded, group.Pending, group.Waiting) {
		t.Fatalf("before ack: %v", got)
	}
	if q := s.Queue(); len(q) != 1 || q[0] != r5 {
		t.Fatalf("queue before ack = %v, want [5]", q)
	}

	if err := s.AckCancel(r1); err != nil {
		t.Fatal(err)
	}
	// r1 Cancelled；r4 晋升入队尾，队列 [r5,r4]；r5 得执行位（r4 提交更早却排在后面）。
	if got := statesOf(s, r1, r4, r5); !sameStates(got,
		group.Cancelled, group.Waiting, group.Running) {
		t.Fatalf("after ack: %v", got)
	}
	if q := s.Queue(); len(q) != 1 || q[0] != r4 {
		t.Fatalf("queue after ack = %v, want [4]", q)
	}

	r6, e := s.Submit(gstr("g"), true, false)
	must(r6, e, 6, "r6")
	if got := statesOf(s, r4, r6); !sameStates(got, group.Cancelled, group.Waiting) {
		t.Fatalf("after r6: %v", got)
	}
	if err := s.Finish(r5, true); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.StateOf(r5); st != group.Succeeded {
		t.Fatalf("r5 = %v", st)
	}
	if st, _ := s.StateOf(r6); st != group.Running {
		t.Fatalf("r6 = %v, want Running", st)
	}

	r7, e := s.Submit(gstr("g"), true, false)
	must(r7, e, 7, "r7")
	if got := statesOf(s, r6, r7); !sameStates(got, group.Cancelling, group.Pending) {
		t.Fatalf("after r7: %v", got)
	}
	// Cancelling 上的迟到 Finish：取消优先，r6 记 Cancelled；r7 晋升并 Running。
	if err := s.Finish(r6, true); err != nil {
		t.Fatal(err)
	}
	if got := statesOf(s, r6, r7); !sameStates(got, group.Cancelled, group.Running) {
		t.Fatalf("after late finish: %v", got)
	}
}

// ---- 题目范例二：Q=0 的净增判定与 Pending 不占队列 ----

func TestExample2_Q0(t *testing.T) {
	s, _ := New(1, 0)
	r1, e := s.Submit(gstr("g"), false, false) // 直接 Running，L1==L0==0
	if e != nil || r1 != 1 {
		t.Fatalf("r1: id=%d err=%v", r1, e)
	}
	if _, err := s.Submit(nil, false, false); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("empty-group submit: err=%v, want ErrQueueFull", err)
	}
	r2, e := s.Submit(gstr("g"), false, false) // Pending 不被拒
	if e != nil || r2 != 2 {
		t.Fatalf("r2: id=%d err=%v", r2, e)
	}
	if st, _ := s.StateOf(r2); st != group.Pending {
		t.Fatalf("r2 = %v", st)
	}
	if err := s.Finish(r1, true); err != nil {
		t.Fatal(err)
	}
	if got := statesOf(s, r1, r2); !sameStates(got, group.Succeeded, group.Running) {
		t.Fatalf("after finish: %v", got)
	}
}

// protected 保护 Running 不被 Submit 顶替，但挡不住用户 Cancel。
func TestProtectedRunning(t *testing.T) {
	s, _ := New(1, 10)
	r1, _ := s.Submit(gstr("g"), false, true)
	r2, err := s.Submit(gstr("g"), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := statesOf(s, r1, r2); !sameStates(got, group.Running, group.Pending) {
		t.Fatalf("protected running superseded: %v", got)
	}
	if err := s.Cancel(r1); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.StateOf(r1); st != group.Cancelling {
		t.Fatalf("r1 = %v", st)
	}
}

// protected 对 Waiting 无效。
func TestProtectedDoesNotProtectWaiting(t *testing.T) {
	s, _ := New(1, 10)
	s.Submit(gstr("a"), false, false)           // Running
	r2, _ := s.Submit(gstr("g"), false, true)   // protected 但仍 Waiting
	r3, err := s.Submit(gstr("g"), true, false) // 顶替 Waiting，无视 protected
	if err != nil {
		t.Fatal(err)
	}
	if got := statesOf(s, r2, r3); !sameStates(got, group.Cancelled, group.Waiting) {
		t.Fatalf("protected waiting: %v", got)
	}
}

// Pending 被连续顶替，最终只有最后一个为 Pending。
func TestPendingChainedSupersede(t *testing.T) {
	s, _ := New(1, 10)
	r1, _ := s.Submit(gstr("g"), false, false)
	r2, _ := s.Submit(gstr("g"), false, false)
	r3, _ := s.Submit(gstr("g"), false, false)
	r4, _ := s.Submit(gstr("g"), false, false)
	if got := statesOf(s, r1, r2, r3, r4); !sameStates(got,
		group.Running, group.Superseded, group.Superseded, group.Pending) {
		t.Fatalf("chained: %v", got)
	}
}

// 顶替标志只取自新提交者：旧提交的 cancel=true 不影响后续判定。
func TestFlagFromNewSubmitter(t *testing.T) {
	s, _ := New(1, 10)
	r1, _ := s.Submit(gstr("g"), true, false)
	r2, _ := s.Submit(gstr("g"), false, false)
	if got := statesOf(s, r1, r2); !sameStates(got, group.Running, group.Pending) {
		t.Fatalf("r2: %v", got)
	}
	r3, _ := s.Submit(gstr("g"), false, false)
	if got := statesOf(s, r1, r2, r3); !sameStates(got,
		group.Running, group.Superseded, group.Pending) {
		t.Fatalf("r3: %v", got)
	}
	r4, _ := s.Submit(gstr("g"), true, false)
	if got := statesOf(s, r1, r3, r4); !sameStates(got,
		group.Cancelling, group.Superseded, group.Pending) {
		t.Fatalf("r4: %v", got)
	}
}

// 队列已满：被拒不占号、不入队、不顶替原 Pending。
func TestQueueFullNoMutation(t *testing.T) {
	s2, _ := New(1, 1)
	g1, _ := s2.Submit(gstr("g"), false, false) // Running
	s2.Submit(gstr("b"), false, false)          // Waiting，队列长度 1
	gp, _ := s2.Submit(gstr("g"), false, false) // Pending（不占队列、不被拒）
	if st, _ := s2.StateOf(gp); st != group.Pending {
		t.Fatalf("gp = %v", st)
	}
	// 队列满时提交新组：被拒，且不顶替 g 组原 Pending。
	if _, err := s2.Submit(gstr("h"), false, false); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("err = %v, want ErrQueueFull", err)
	}
	if st, _ := s2.StateOf(gp); st != group.Pending {
		t.Fatalf("gp = %v, want Pending（被拒不顶替原 Pending）", st)
	}
	// 队列仍是 [b]，编号未被被拒提交占用。
	if q := s2.Queue(); len(q) != 1 || q[0] != 2 {
		t.Fatalf("queue = %v, want [2]", q)
	}
	if err := s2.Finish(g1, true); err != nil {
		t.Fatal(err)
	}
	// g1 结束：Release 后队列 [b]；promote gp -> [b,gp]（长度 2 > Q=1，允许）；
	// allocate 取队首 b -> Running，gp 等待。b 结束后 gp Running，
	// 此时队列空，h 可提交，编号仍为 4（被拒不占号）。
	if err := s2.Finish(2, true); err != nil {
		t.Fatal(err)
	}
	r4, err := s2.Submit(gstr("h"), false, false)
	if err != nil {
		t.Fatal(err)
	}
	if r4 != 4 {
		t.Fatalf("next id = %d, want 4（被拒提交不占号）", r4)
	}
}

// 顶掉 Waiting 占位者再入队净增为 0，队列满也不拒。
func TestReplaceWaitingNetZero(t *testing.T) {
	s, _ := New(1, 1)
	s.Submit(gstr("a"), false, false)           // Running
	s.Submit(gstr("g"), false, false)           // Waiting（队列长度 1=Q）
	r3, err := s.Submit(gstr("g"), true, false) // 一出一入
	if err != nil {
		t.Fatalf("net-zero submit rejected: %v", err)
	}
	if q := s.Queue(); len(q) != 1 || q[0] != r3 {
		t.Fatalf("queue = %v, want [%d]", q, r3)
	}
}

// Pending 晋升不受 Q 约束，队列可暂时超过 Q。
func TestPromotionCanExceedQ(t *testing.T) {
	s, _ := New(2, 1)
	a, _ := s.Submit(gstr("a"), false, false)  // Running
	gp, _ := s.Submit(gstr("g"), false, false) // Running（两个执行位占满）
	gq, _ := s.Submit(gstr("g"), false, false) // Pending
	b, _ := s.Submit(gstr("b"), false, false)  // Waiting，队列长度 1=Q
	// gp Cancelling 仍占执行位；a Finish 释放一个位 -> b 立即 Running（队列空）。
	if err := s.Cancel(gp); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(a, true); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.StateOf(b); st != group.Running {
		t.Fatalf("b = %v", st)
	}
	// 此刻执行位：gp(Cancelling)+b(Running)，队列空。塞入一个 Waiting 占满 Q：
	c, err := s.Submit(gstr("c"), false, false)
	if err != nil {
		t.Fatal(err)
	}
	// AckCancel(gp)：gq 先晋升入队尾（队列 [c,gq]，长度 2 > Q=1），
	// 然后只释放了一个位 -> 队首 c 得位，gq 仍 Waiting；最终队列长度 1，
	// 但内部分配发生在队列长度 2 之上。为精确观测「可超 Q」，这里直接验证
	// 晋升路径不校验 Q：用 C=1 时队列可达长度 2 的瞬态由 allocate 内部产生，
	// 外部可验证的是结果次序：gq 在 c 之后。
	if err := s.AckCancel(gp); err != nil {
		t.Fatal(err)
	}
	if peak := s.lastPeakQueue(); peak <= 1 {
		t.Fatalf("peak queue during AckCancel = %d, want > Q=1（晋升可超 Q）", peak)
	}
	if st, _ := s.StateOf(c); st != group.Running {
		t.Fatalf("c = %v, want Running（晋升者排队尾）", st)
	}
	if st, _ := s.StateOf(gq); st != group.Waiting {
		t.Fatalf("gq = %v, want Waiting（曾与 c 同时在 Q=1 的队列中）", st)
	}
	if q := s.Queue(); len(q) != 1 || q[0] != gq {
		t.Fatalf("queue = %v, want [%d]", q, gq)
	}
}

// ---- 拒绝次序：参数非法 > 运行不存在 > 状态不符 ----

func TestRejectOrder(t *testing.T) {
	long := make([]byte, 65)
	// 参数非法（编号非正、组名过长、C/Q 越界）优先于一切。
	if _, err := New(0, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("New(0,0) err=%v", err)
	}
	if _, err := New(1, -1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("New(1,-1) err=%v", err)
	}

	cases := []struct {
		name string
		call func(s *Scheduler) error
		want error
		why  string
	}{
		{"finish-id-0", func(s *Scheduler) error { return s.Finish(0, true) }, ErrInvalid,
			"编号非正优先于不存在"},
		{"cancel-id-neg", func(s *Scheduler) error { return s.Cancel(-3) }, ErrInvalid,
			"编号非正优先于不存在"},
		{"ack-id-0", func(s *Scheduler) error { return s.AckCancel(0) }, ErrInvalid,
			"编号非正优先于不存在"},
		{"finish-missing", func(s *Scheduler) error { return s.Finish(99, true) }, ErrNotFound,
			"不存在优先于状态不符"},
		{"ack-missing", func(s *Scheduler) error { return s.AckCancel(99) }, ErrNotFound, ""},
		{"cancel-missing", func(s *Scheduler) error { return s.Cancel(99) }, ErrNotFound, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := New(2, 10)
			if err := tc.call(s); !errors.Is(err, tc.want) {
				t.Fatalf("%s: got %v want %v（%s）", tc.name, err, tc.want, tc.why)
			}
		})
	}

	// 参数非法（组名过长）优先于队列已满：把队列塞满再提交超长组名。
	sFull, _ := New(1, 0)
	sFull.Submit(gstr("a"), false, false)
	if _, err := sFull.Submit(long, false, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("long group while full: err=%v, want ErrInvalid", err)
	}

	// 状态不符：每个子用例使用独立调度器，避免时序耦合。
	stateCases := []struct {
		name  string
		setup func(s *Scheduler) int // 返回被测运行 id
		call  func(s *Scheduler, id int) error
		want  error
	}{
		{
			"finish-terminal",
			func(s *Scheduler) int {
				id, _ := s.Submit(gstr("a"), false, false)
				s.Finish(id, true) // Succeeded
				return id
			},
			func(s *Scheduler, id int) error { return s.Finish(id, true) },
			ErrState,
		},
		{
			"finish-waiting",
			func(s *Scheduler) int {
				s.Submit(gstr("a"), false, false)          // Running，占住唯一位
				id, _ := s.Submit(gstr("b"), false, false) // Waiting
				return id
			},
			func(s *Scheduler, id int) error { return s.Finish(id, true) },
			ErrState,
		},
		{
			"finish-pending",
			func(s *Scheduler) int {
				s.Submit(gstr("a"), false, false)
				id, _ := s.Submit(gstr("a"), false, false) // Pending
				return id
			},
			func(s *Scheduler, id int) error { return s.Finish(id, true) },
			ErrState,
		},
		{
			"ack-running",
			func(s *Scheduler) int {
				id, _ := s.Submit(gstr("a"), false, false) // Running
				return id
			},
			func(s *Scheduler, id int) error { return s.AckCancel(id) },
			ErrState,
		},
		{
			"ack-pending",
			func(s *Scheduler) int {
				s.Submit(gstr("a"), false, false)
				id, _ := s.Submit(gstr("a"), false, false) // Pending
				return id
			},
			func(s *Scheduler, id int) error { return s.AckCancel(id) },
			ErrState,
		},
		{
			"cancel-terminal",
			func(s *Scheduler) int {
				id, _ := s.Submit(gstr("a"), false, false)
				s.Finish(id, false) // Failed
				return id
			},
			func(s *Scheduler, id int) error { return s.Cancel(id) },
			ErrState,
		},
		{
			"cancel-cancelling",
			func(s *Scheduler) int {
				id, _ := s.Submit(gstr("x"), false, false)
				s.Cancel(id) // Running -> Cancelling
				return id
			},
			func(s *Scheduler, id int) error { return s.Cancel(id) },
			ErrState,
		},
		{
			"ack-twice",
			func(s *Scheduler) int {
				id, _ := s.Submit(gstr("x"), false, false)
				s.Cancel(id)
				s.AckCancel(id) // Cancelled
				return id
			},
			func(s *Scheduler, id int) error { return s.AckCancel(id) },
			ErrState,
		},
		{
			"finish-cancelled",
			func(s *Scheduler) int {
				id, _ := s.Submit(gstr("x"), false, false)
				s.Cancel(id)
				s.AckCancel(id) // Cancelled
				return id
			},
			func(s *Scheduler, id int) error { return s.Finish(id, false) },
			ErrState,
		},
	}
	for _, tc := range stateCases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := New(1, 10)
			id := tc.setup(s)
			if err := tc.call(s, id); !errors.Is(err, tc.want) {
				t.Fatalf("%s: got %v want %v", tc.name, err, tc.want)
			}
		})
	}
}

// Cancelling 上迟到的 Finish(ok=false) 仍记 Cancelled。
func TestLateFinishOnCancelling(t *testing.T) {
	s, _ := New(1, 10)
	r1, _ := s.Submit(gstr("g"), false, false)
	r2, _ := s.Submit(gstr("g"), true, false) // r1 Cancelling, r2 Pending
	if got := statesOf(s, r1, r2); !sameStates(got, group.Cancelling, group.Pending) {
		t.Fatalf("pre: %v", got)
	}
	if err := s.Finish(r1, false); err != nil {
		t.Fatal(err)
	}
	if got := statesOf(s, r1, r2); !sameStates(got, group.Cancelled, group.Running) {
		t.Fatalf("late finish: %v", got)
	}
}

// Finish(running, false) -> Failed；Cancel Waiting 出队后队首推进。
func TestFailedAndCancelWaiting(t *testing.T) {
	s, _ := New(1, 3)
	r1, _ := s.Submit(gstr("a"), false, false)
	r2, _ := s.Submit(gstr("b"), false, false)
	r3, _ := s.Submit(gstr("c"), false, false)
	if err := s.Cancel(r2); err != nil { // 取消队列中部，O(1) 移除
		t.Fatal(err)
	}
	if q := s.Queue(); len(q) != 1 || q[0] != r3 {
		t.Fatalf("queue after mid cancel = %v, want [%d]", q, r3)
	}
	if err := s.Finish(r1, false); err != nil {
		t.Fatal(err)
	}
	if got := statesOf(s, r1, r2, r3); !sameStates(got,
		group.Failed, group.Cancelled, group.Running) {
		t.Fatalf("states: %v", got)
	}
}
