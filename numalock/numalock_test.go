package numalock

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// ---------- test helpers ----------

type call struct {
	op  string
	tid int
}

func (c call) String() string { return c.op + "(" + itoa(c.tid) + ")" }

func applyReal(l *Lock, c call) Result {
	switch c.op {
	case "RLock":
		return l.RLock(c.tid)
	case "RUnlock":
		return l.RUnlock(c.tid)
	case "WLock":
		return l.WLock(c.tid)
	case "WUnlock":
		return l.WUnlock(c.tid)
	case "Downgrade":
		return l.Downgrade(c.tid)
	case "Upgrade":
		return l.Upgrade(c.tid)
	default:
		return l.Cancel(c.tid)
	}
}

func applyNaive(n *naiveModel, c call) naiveResult {
	switch c.op {
	case "RLock":
		return n.RLock(c.tid)
	case "RUnlock":
		return n.RUnlock(c.tid)
	case "WLock":
		return n.WLock(c.tid)
	case "WUnlock":
		return n.WUnlock(c.tid)
	case "Downgrade":
		return n.Downgrade(c.tid)
	case "Upgrade":
		return n.Upgrade(c.tid)
	default:
		return n.Cancel(c.tid)
	}
}

func sortedCopy(x []int) []int {
	y := append([]int(nil), x...)
	sort.Ints(y)
	return y
}

func eqInts(a, b []int) bool {
	if len(a) == 0 {
		a = nil
	}
	if len(b) == 0 {
		b = nil
	}
	return reflect.DeepEqual(a, b)
}

// compareSnapshots compares everything; R is a set (unordered) in the real
// lock, Qr/Qw/G are ordered FIFO queues.
func compareSnapshots(a, b Snapshot) error {
	if a.Phase != b.Phase {
		return fmt.Errorf("phase %s != %s", a.Phase, b.Phase)
	}
	if !eqInts(sortedCopy(a.R), sortedCopy(b.R)) {
		return fmt.Errorf("R %v != %v", a.R, b.R)
	}
	if a.W != b.W {
		return fmt.Errorf("w %d != %d", a.W, b.W)
	}
	if a.Gown != b.Gown {
		return fmt.Errorf("gown %d != %d", a.Gown, b.Gown)
	}
	if a.P != b.P {
		return fmt.Errorf("p %d != %d", a.P, b.P)
	}
	if !eqInts(a.Qr, b.Qr) {
		return fmt.Errorf("Qr %v != %v", a.Qr, b.Qr)
	}
	if len(a.Qw) != len(b.Qw) {
		return fmt.Errorf("Qw len %d != %d", len(a.Qw), len(b.Qw))
	}
	for i := range a.Qw {
		if !eqInts(a.Qw[i], b.Qw[i]) {
			return fmt.Errorf("Qw[%d] %v != %v", i, a.Qw[i], b.Qw[i])
		}
	}
	if !eqInts(a.G, b.G) {
		return fmt.Errorf("G %v != %v", a.G, b.G)
	}
	if !reflect.DeepEqual(a.States, b.States) {
		return fmt.Errorf("states %v != %v", a.States, b.States)
	}
	return nil
}

// checkInvariants verifies every structural invariant from the spec.
func checkInvariants(l *Lock, s Snapshot) error {
	switch s.Phase {
	case PhaseWrite:
		if !(s.W != -1 && len(s.R) == 0) {
			return fmt.Errorf("写相位要求 w 非空且 R 空, got w=%d R=%v", s.W, s.R)
		}
	case PhaseRead:
		if len(s.R) == 0 {
			return errors.New("读相位要求 R 非空")
		}
		if s.W != -1 {
			return fmt.Errorf("读相位 w 必须为空, got %d", s.W)
		}
	case PhaseIdle:
		if len(s.R) != 0 || s.W != -1 || len(s.Qr) != 0 {
			return fmt.Errorf("空闲要求 R/Qr/w 皆空, R=%v w=%d Qr=%v", s.R, s.W, s.Qr)
		}
		for n, q := range s.Qw {
			if len(q) != 0 {
				return fmt.Errorf("空闲要求 Qw[%d] 空, got %v", n, q)
			}
		}
		if len(s.G) != 0 {
			return fmt.Errorf("空闲要求 G 空, got %v", s.G)
		}
	}

	// state/container consistency
	for tid, st := range s.States {
		inR := false
		for _, x := range s.R {
			if x == tid {
				inR = true
			}
		}
		inQr := false
		for _, x := range s.Qr {
			if x == tid {
				inQr = true
			}
		}
		inQw := false
		for _, q := range s.Qw {
			for _, x := range q {
				if x == tid {
					inQw = true
				}
			}
		}
		switch st {
		case StateReadHold:
			if !inR {
				return fmt.Errorf("线程 %d 读持有但不在 R", tid)
			}
		case StateWriteHold:
			if s.W != tid {
				return fmt.Errorf("线程 %d 写持有但 w=%d", tid, s.W)
			}
		case StateReadWait:
			if !inQr {
				return fmt.Errorf("线程 %d 读等待但不在 Qr", tid)
			}
		case StateWriteWait:
			if !inQw {
				return fmt.Errorf("线程 %d 写等待但不在 Qw", tid)
			}
		case StateIdle:
			if inR || inQr || inQw || s.W == tid {
				return fmt.Errorf("线程 %d 空闲却仍在某容器中", tid)
			}
		}
	}

	// G membership rule
	seen := map[int]bool{}
	for _, n := range s.G {
		if seen[n] {
			return fmt.Errorf("G 含重复节点 %d: %v", n, s.G)
		}
		seen[n] = true
	}
	for n := 0; n < l.m; n++ {
		nonEmpty := len(s.Qw[n]) > 0
		inG := seen[n]
		switch s.Phase {
		case PhaseWrite:
			want := nonEmpty && n != s.Gown
			if inG != want {
				return fmt.Errorf("写相位节点 %d: 非空Qw=%v gown=%d, G 成员=%v", n, nonEmpty, s.Gown, inG)
			}
		case PhaseRead, PhaseIdle:
			want := nonEmpty
			if inG != want {
				return fmt.Errorf("相位 %s 节点 %d: 非空Qw=%v, G 成员=%v", s.Phase, n, nonEmpty, inG)
			}
		}
	}

	// Qr non-empty implies write phase, or read phase with a waiting writer
	if len(s.Qr) > 0 {
		ok := s.Phase == PhaseWrite || (s.Phase == PhaseRead && hasNonEmpty(s.Qw))
		if !ok {
			return fmt.Errorf("Qr 非空但相位=%s 且写者等待=%v", s.Phase, hasNonEmpty(s.Qw))
		}
	}

	// local handoff counter never exceeds B
	if s.P > l.b {
		return fmt.Errorf("p=%d 超过 B=%d", s.P, l.b)
	}
	return nil
}

func hasNonEmpty(qw [][]int) bool {
	for _, q := range qw {
		if len(q) > 0 {
			return true
		}
	}
	return false
}

// ---------- spec worked example ----------

func newExample(t *testing.T) *Lock {
	t.Helper()
	// M=2, B=1; threads 0,1,4 on node 0; threads 2,3,5 on node 1.
	nodeOf := []int{0, 0, 1, 1, 0, 1}
	l, err := New(2, 6, nodeOf, 1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return l
}

func mustGranted(t *testing.T, r Result, want []int, ctx string) {
	t.Helper()
	if !r.OK || !eqInts(r.Granted, want) {
		t.Fatalf("%s: 授予=%v(ok=%v) 期望 %v; 依据=%s", ctx, r.Granted, r.OK, want, r.Reason)
	}
}

func mustReject(t *testing.T, r Result, ctx string) {
	t.Helper()
	if r.OK {
		t.Fatalf("%s: 应拒绝却成功, 授予=%v", ctx, r.Granted)
	}
}

// TestSpecExample replays the exact scenario given in the task description.
func TestSpecExample(t *testing.T) {
	l := newExample(t)
	var log bytes.Buffer
	step := func(c call, wantGrant []int) {
		r := applyReal(l, c)
		fmt.Fprintf(&log, "%-12s -> ok=%v granted=%v | %s\n", c, r.OK, r.Granted, r.Reason)
		if wantGrant != nil && (!r.OK || !eqInts(r.Granted, wantGrant)) {
			t.Fatalf("%s: 授予 %v, 期望 %v\n%s", c, r.Granted, wantGrant, log.String())
		}
		if err := checkInvariants(l, l.Snapshot()); err != nil {
			t.Fatalf("%s 后不变量被破坏: %v\n%s", c, err, log.String())
		}
	}

	step(call{"WLock", 0}, []int{0}) // 0 写授予
	step(call{"RLock", 1}, nil)      // 1 入 Qr
	step(call{"WLock", 2}, nil)      // Qw[1]=[2], G=[1]
	step(call{"WLock", 3}, nil)      // Qw[1]=[2,3]
	step(call{"WLock", 4}, nil)      // Qw[0]=[4], gown=0 不入 G
	step(call{"WLock", 5}, nil)      // Qw[1]=[2,3,5]

	s := l.Snapshot()
	if !eqInts(s.Qr, []int{1}) || !eqInts(s.G, []int{1}) ||
		!eqInts(s.Qw[0], []int{4}) || !eqInts(s.Qw[1], []int{2, 3, 5}) {
		t.Fatalf("初始排队状态错误: Qr=%v G=%v Qw=%v", s.Qr, s.G, s.Qw)
	}

	step(call{"WUnlock", 0}, []int{1}) // ① 读批授予 1; G=[1,0]
	s = l.Snapshot()
	if !eqInts(s.G, []int{1, 0}) {
		t.Fatalf("gown 重入位置错误: G=%v 期望 [1 0]", s.G)
	}

	step(call{"RUnlock", 1}, []int{2}) // 起写: 线程 2, G=[0]
	s = l.Snapshot()
	if s.W != 2 || s.Gown != 1 || s.P != 0 || !eqInts(s.G, []int{0}) {
		t.Fatalf("起写后状态错误: w=%d gown=%d p=%d G=%v", s.W, s.Gown, s.P, s.G)
	}

	step(call{"WUnlock", 2}, []int{3}) // ② p=0<1 本地传给 3, p=1
	s = l.Snapshot()
	if s.W != 3 || s.P != 1 {
		t.Fatalf("本地传递后 w=%d p=%d", s.W, s.P)
	}

	step(call{"WUnlock", 3}, []int{4}) // ③ p==B, gown 重入; 起写 4
	s = l.Snapshot()
	if s.W != 4 || s.Gown != 0 || s.P != 0 || !eqInts(s.G, []int{1}) {
		t.Fatalf("跨节点交接后 w=%d gown=%d p=%d G=%v (期望 w=4 gown=0 G=[1])",
			s.W, s.Gown, s.P, s.G)
	}
	if !eqInts(s.Qw[1], []int{5}) {
		t.Fatalf("Qw[1]=%v 期望 [5]", s.Qw[1])
	}
	t.Logf("\n规格示例逐步日志:\n%s", log.String())
}

// ---------- constructor validation ----------

func TestBadConfig(t *testing.T) {
	bad := []struct {
		m, t, b int
		nodeOf  []int
	}{
		{0, 4, 1, []int{0, 0, 0, 0}},
		{9, 4, 1, []int{0, 0, 0, 0}},
		{2, 0, 1, []int{}},
		{2, 33, 1, make([]int, 33)},
		{2, 3, 0, []int{0, 1, 1}},
		{2, 3, 17, []int{0, 1, 1}},
		{2, 3, 1, []int{0, 1}},     // 长度不符
		{2, 3, 1, []int{0, 1, 2}},  // 节点越界
		{2, 3, 1, []int{0, -1, 1}}, // 节点非法
	}
	for i, c := range bad {
		if _, err := New(c.m, c.t, c.nodeOf, c.b); err == nil {
			t.Fatalf("用例 %d 配置 %+v 应被整体拒绝", i, c)
		}
	}
}

// ---------- rejection ordering: tid first, then state, then upgrade conflict ----------

func TestRejectionOrder(t *testing.T) {
	l := newExample(t)
	r := l.RLock(99)
	mustReject(t, r, "越界线程 RLock")
	if !strings.Contains(r.Reason, "线程号越界") {
		t.Fatalf("拒绝原因应为线程号越界, got %q", r.Reason)
	}
	r = l.Cancel(-1)
	mustReject(t, r, "负线程号")

	// thread 0 holds write; wrong-state calls keep state untouched.
	mustGranted(t, l.WLock(0), []int{0}, "WLock 0")
	for _, c := range []call{{"RLock", 0}, {"WLock", 0}, {"RUnlock", 0},
		{"WUnlock", 1}, {"Downgrade", 1}, {"Upgrade", 0}, {"Cancel", 0}} {
		before := l.Snapshot()
		r := applyReal(l, c)
		mustReject(t, r, c.String())
		if err := compareSnapshots(before, l.Snapshot()); err != nil {
			t.Fatalf("被拒绝调用 %s 改变了状态: %v", c, err)
		}
	}

	// upgrade conflict: multiple readers -> rejected even though tid holds read
	l3, _ := New(1, 2, []int{0, 0}, 1)
	mustGranted(t, l3.RLock(0), []int{0}, "读 0")
	mustGranted(t, l3.RLock(1), []int{1}, "读 1")
	before := l3.Snapshot()
	mustReject(t, l3.Upgrade(0), "多读者升级")
	if err := compareSnapshots(before, l3.Snapshot()); err != nil {
		t.Fatalf("升级冲突后状态被改变: %v", err)
	}
}
