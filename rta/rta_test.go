package rta

import (
	"strings"
	"testing"
)

func mustAdd(t *testing.T, s *Scheduler, task Task, wantReordered bool) {
	t.Helper()
	res, err := s.Add(task)
	if err != nil {
		t.Fatalf("Add(%s) unexpected error: %v", task.ID, err)
	}
	if res.Reordered != wantReordered {
		t.Fatalf("Add(%s) Reordered=%v want %v; order=%v", task.ID, res.Reordered, wantReordered, s.Order())
	}
}

func errReason(t *testing.T, err error, want Reason, wantUnassigned int) {
	t.Helper()
	re, ok := err.(*Error)
	if !ok {
		t.Fatalf("want *rta.Error, got %T: %v", err, err)
	}
	if re.Reason != want || re.Unassigned != wantUnassigned {
		t.Fatalf("reason=%d(unassigned=%d), want %d(unassigned=%d)",
			re.Reason, re.Unassigned, want, wantUnassigned)
	}
}

func resp(t *testing.T, s *Scheduler, id string, want int64) {
	t.Helper()
	got, err := s.Response(id)
	if err != nil {
		t.Fatalf("Response(%s): %v", id, err)
	}
	if got != want {
		t.Fatalf("Response(%s)=%d, want %d; order=%v", id, got, want, s.Order())
	}
}

// 例 1：A、B 依次加入得 [A,B]，R_A=1、R_B=4。
func TestExampleAB(t *testing.T) {
	s := New()
	mustAdd(t, s, Task{ID: "A", C: 1, T: 12, D: 10}, false)
	mustAdd(t, s, Task{ID: "B", C: 3, T: 6, D: 6}, false)
	if got := s.Order(); strings.Join(got, ",") != "A,B" {
		t.Fatalf("order=%v want [A B]", got)
	}
	resp(t, s, "A", 1)
	resp(t, s, "B", 4)
}

// E 插入最低位失败，次低位成功：[A,E,B] 而非把 E 提到最高。
func TestExampleEInsertionKeepsRelativeOrder(t *testing.T) {
	s := New()
	mustAdd(t, s, Task{ID: "A", C: 1, T: 12, D: 10}, false)
	mustAdd(t, s, Task{ID: "B", C: 3, T: 6, D: 6}, false)

	// 先核对 [A,B,E] 中 E 不可调度：w: 1->4->5，5>D=2。
	if _, _, ok := analyzeTask(Task{ID: "E", C: 1, T: 6, D: 2},
		[]Task{{ID: "A", C: 1, T: 12, D: 10}, {ID: "B", C: 3, T: 6, D: 6}}); ok {
		t.Fatal("E at lowest priority must be unschedulable")
	}

	mustAdd(t, s, Task{ID: "E", C: 1, T: 6, D: 2}, false)
	if got := s.Order(); strings.Join(got, ",") != "A,E,B" {
		t.Fatalf("order=%v want [A E B]", got)
	}
	resp(t, s, "A", 1)
	resp(t, s, "E", 2)
	resp(t, s, "B", 5)
}

// C 三处插入全败，Audsley 回退，原相对次序被颠倒：[C,B,A]，标记重排。
func TestExampleCAudsleyFallback(t *testing.T) {
	s := New()
	mustAdd(t, s, Task{ID: "A", C: 1, T: 12, D: 10}, false)
	mustAdd(t, s, Task{ID: "B", C: 3, T: 6, D: 6}, false)

	res, err := s.Add(Task{ID: "C", C: 3, T: 10, D: 9, B: 2})
	if err != nil {
		t.Fatalf("Add(C) unexpected error: %v", err)
	}
	if !res.Reordered {
		t.Fatal("Add(C) must report Reordered=true")
	}
	if got := s.Order(); strings.Join(got, ",") != "C,B,A" {
		t.Fatalf("order=%v want [C B A]", got)
	}
	resp(t, s, "C", 5) // w=5: 2+ceil(5/6)*3=5 不动点；R=5
	resp(t, s, "B", 6) // 受 C 干扰：w:3->6，R=6
	resp(t, s, "A", 10)
}

// w+J 恰等于 D 可调度，大 1 不可调度。
func TestDeadlineEqualityBoundary(t *testing.T) {
	task := Task{ID: "i", C: 2, T: 10, D: 4, J: 2}
	if r, _, ok := analyzeTask(task, nil); !ok || r != 4 {
		t.Fatalf("boundary equal: r=%d ok=%v, want 4,true", r, ok)
	}
	task.D = 3
	if _, steps, ok := analyzeTask(task, nil); ok || steps != 1 {
		t.Fatalf("boundary over: ok=%v steps=%d, want false,1", ok, steps)
	}
}

// 抖动放在 w 内部：干扰者抖动进入 ceil((w+J_j)/T_j)。
// G(C=2,T=10,D=10,J=8) 在 H(C=3,T=7,D=7) 之上时，H: w=3->5->7，R=7。
func TestJitterInsideInterference(t *testing.T) {
	g := Task{ID: "G", C: 2, T: 10, D: 10, J: 8}
	h := Task{ID: "H", C: 3, T: 7, D: 7}
	if r, _, ok := analyzeTask(h, []Task{g}); !ok || r != 7 {
		t.Fatalf("H under G: r=%d ok=%v, want 7,true", r, ok)
	}
	g0 := Task{ID: "G", C: 2, T: 10, D: 10, J: 0}
	if r, _, ok := analyzeTask(h, []Task{g0}); !ok || r != 5 {
		t.Fatalf("J=0: r=%d ok=%v, want 5,true", r, ok)
	}
	hj := Task{ID: "H", C: 3, T: 7, D: 7, J: 1}
	if _, _, ok := analyzeTask(hj, []Task{g}); ok {
		t.Fatal("H with own J=1 must be unschedulable (R=8>7)")
	}
}

// B 参与初值：w 自 C+B 起。
func TestBlockingInInitialValue(t *testing.T) {
	task := Task{ID: "X", C: 3, T: 100, D: 5, B: 2}
	if r, steps, ok := analyzeTask(task, nil); !ok || r != 5 || steps != 1 {
		t.Fatalf("blocking: r=%d steps=%d ok=%v, want 5,1,true", r, steps, ok)
	}
	task.D = 4
	if _, _, ok := analyzeTask(task, nil); ok {
		t.Fatal("C+B=5>D=4 must fail at initial value")
	}
}

// 128 位防溢出：白盒构造需求爆炸的干扰集，验证饱和分支安全判负
// （合法输入下 C≤D≤T 使真实需求有界，此路径为规格要求的防御）。
func TestOverflowSaturation(t *testing.T) {
	target := Task{ID: "i", C: 1, T: 1_000_000_000, D: 1_000_000_000}
	higher := []Task{{ID: "j", C: 1_000_000_000, T: 1}}
	if d, sat := demand(maxDemand, target, higher); !sat || d != maxDemand {
		t.Fatalf("demand saturation: d=%d sat=%v", d, sat)
	}
	if _, _, ok := analyzeTask(target, higher); ok {
		t.Fatal("saturated demand must be unschedulable")
	}

	// 端到端：合法任务集 {i,j} 任何序都不可调度（j: C=1e9,T=D=1e9）。
	s := New()
	mustAdd(t, s, Task{ID: "j", C: 1_000_000_000, T: 1_000_000_000, D: 1_000_000_000}, false)
	_, err := s.Add(target)
	errReason(t, err, ReasonUnschedulable, 2)
}

// Audsley 同一位有多个候选时取编号字节序最小者。
func TestAudsleyPicksLexicographicallySmallest(t *testing.T) {
	a := Task{ID: "zeta", C: 1, T: 10, D: 10}
	b := Task{ID: "alpha", C: 1, T: 10, D: 10}
	order, _, ok := audsley([]Task{a, b})
	if !ok {
		t.Fatal("audsley failed")
	}
	// 最低位 = alpha（字节序小），故高到低 = [zeta, alpha]。
	if order[0].ID != "zeta" || order[1].ID != "alpha" {
		t.Fatalf("audsley order=%v want [zeta alpha]", []string{order[0].ID, order[1].ID})
	}
}

// 插入全败且 Audsley 也失败：拒绝携带未分配个数，且状态不变。
func TestRejectUnschedulableUnassignedCount(t *testing.T) {
	s := New()
	mustAdd(t, s, Task{ID: "A", C: 4, T: 8, D: 8}, false)
	mustAdd(t, s, Task{ID: "B", C: 4, T: 8, D: 8}, false)
	// 三个 C=4,T=8,D=8 的任务：最低位 w 必达 12>8，
	// Audsley 填最低位时无人可调度 => Unassigned=3。
	_, err := s.Add(Task{ID: "C", C: 4, T: 8, D: 8})
	errReason(t, err, ReasonUnschedulable, 3)
	if got := s.Order(); strings.Join(got, ",") != "A,B" {
		t.Fatalf("failed Add must not change order, got %v", got)
	}
	if _, err := s.Response("C"); err == nil {
		t.Fatal("failed Add must not register the task")
	}
}

// 容量满。
func TestCapacityFull(t *testing.T) {
	s := New()
	for i := 0; i < MaxTasks; i++ {
		mustAdd(t, s, Task{ID: "t" + string(rune('a'+i)), C: 1, T: 1000, D: 1000}, false)
	}
	_, err := s.Add(Task{ID: "x", C: 1, T: 1000, D: 1000})
	errReason(t, err, ReasonCapacityFull, 0)
}

// Remove 后次序不变，后续响应时间重算。
func TestRemoveKeepsOrder(t *testing.T) {
	s := New()
	mustAdd(t, s, Task{ID: "A", C: 1, T: 12, D: 10}, false)
	mustAdd(t, s, Task{ID: "E", C: 1, T: 6, D: 2}, false)
	mustAdd(t, s, Task{ID: "B", C: 3, T: 6, D: 6}, false)
	if err := s.Remove("E"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := s.Order(); strings.Join(got, ",") != "A,B" {
		t.Fatalf("after Remove order=%v want [A B]", got)
	}
	resp(t, s, "A", 1)
	resp(t, s, "B", 4)
	errReason(t, s.Remove("E"), ReasonNotFound, 0)
}

// 拒绝优先级与参数校验。
func TestRejectionPriorityAndValidation(t *testing.T) {
	s := New()
	mustAdd(t, s, Task{ID: "A", C: 1, T: 5, D: 5}, false)

	errReason(t, s.Remove(""), ReasonInvalidParam, 0)
	_, err := s.Response("")
	errReason(t, err, ReasonInvalidParam, 0)
	_, err = s.Add(Task{ID: "A", C: 1, T: 5, D: 5})
	errReason(t, err, ReasonDuplicateID, 0)
	errReason(t, s.Remove("nope"), ReasonNotFound, 0)
	_, err = s.Response("nope")
	errReason(t, err, ReasonNotFound, 0)

	bad := []Task{
		{ID: "", C: 1, T: 5, D: 5},
		{ID: strings.Repeat("x", 65), C: 1, T: 5, D: 5},
		{ID: "z", C: 0, T: 5, D: 5},
		{ID: "z", C: 2, T: 5, D: 5, B: 1_000_000_001},
		{ID: "z", C: 1, T: 0, D: 0},
		{ID: "z", C: 1, T: 5, D: 0},
		{ID: "z", C: 1, T: 5, D: 6},
		{ID: "z", C: 1, T: 5, D: 5, J: -1},
	}
	for i, b := range bad {
		_, err := s.Add(b)
		if err == nil {
			t.Fatalf("bad task #%d accepted: %+v", i, b)
		} else if re := err.(*Error); re.Reason != ReasonInvalidParam {
			t.Fatalf("bad task #%d reason=%d want invalid", i, re.Reason)
		}
	}
}

// Response 不执行任何不动点迭代；且插入/删除只重算规定范围。
func TestCacheAndRecomputeDiscipline(t *testing.T) {
	s := New()
	mustAdd(t, s, Task{ID: "A", C: 1, T: 12, D: 10}, false)
	mustAdd(t, s, Task{ID: "B", C: 3, T: 6, D: 6}, false)

	s.fpSteps = 0
	if _, err := s.Response("A"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Response("B"); err != nil {
		t.Fatal(err)
	}
	if s.fpSteps != 0 {
		t.Fatalf("Response performed %d fixed-point steps, want 0", s.fpSteps)
	}

	// E 在位置 p=1 插入成功：只允许重算 p 及其之后的任务（E、B）。
	s.fpSteps = 0
	s.fpTerms = 0
	s.applyCalls = 0
	mustAdd(t, s, Task{ID: "E", C: 1, T: 6, D: 2}, false)
	if s.applyCalls != 2 {
		t.Fatalf("applyOrder recomputed %d tasks, want 2 (E,B)", s.applyCalls)
	}
	// 精确核对“每个任务重算的不动点单步求和项数恰等于它之前的任务个数”。
	ord := s.Order() // [A E B]
	tasks := map[string]Task{
		"A": {ID: "A", C: 1, T: 12, D: 10},
		"E": {ID: "E", C: 1, T: 6, D: 2},
		"B": {ID: "B", C: 3, T: 6, D: 6},
	}
	pos := map[string]int{"A": 0, "E": 1, "B": 2}
	var wantTerms int
	for _, id := range []string{"E", "B"} { // applyOrder 从 p=1 起重算
		higher := []Task{}
		for _, hid := range ord[:pos[id]] {
			higher = append(higher, tasks[hid])
		}
		_, steps, ok := analyzeTask(tasks[id], higher)
		if !ok {
			t.Fatalf("expected %s schedulable", id)
		}
		wantTerms += steps * len(higher)
	}
	if s.fpTerms != wantTerms {
		t.Fatalf("fpTerms=%d want %d", s.fpTerms, wantTerms)
	}

	// Remove 只重算排在被删任务之后的任务。
	s.fpSteps = 0
	s.fpTerms = 0
	s.applyCalls = 0
	if err := s.Remove("A"); err != nil { // 删最高位：E、B 都需重算
		t.Fatal(err)
	}
	if s.applyCalls != 2 {
		t.Fatalf("Remove(A) recomputed %d tasks, want 2", s.applyCalls)
	}
	s.applyCalls = 0
	if err := s.Remove("B"); err != nil { // 删最低位：0 个
		t.Fatal(err)
	}
	if s.applyCalls != 0 {
		t.Fatalf("Remove(lowest) recomputed %d tasks, want 0", s.applyCalls)
	}
}
