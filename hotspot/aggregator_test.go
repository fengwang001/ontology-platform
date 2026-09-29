package hotspot

import (
	"log"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type testLogger struct {
	t *testing.T
	l *log.Logger
}

func (tl testLogger) Printf(format string, args ...any) {
	tl.t.Logf(format, args...)
	tl.l.Printf(format, args...)
}

func newLoggedAggregator(t *testing.T, cfg Config) *Aggregator {
	t.Helper()
	logger := log.New(os.Stdout, "["+t.Name()+"] ", log.LstdFlags|log.Lmicroseconds)
	return NewWithConfig(cfg).WithLogger(testLogger{t: t, l: logger})
}

func findFunction(s Snapshot, name string) FunctionStat {
	for _, fs := range s.Functions {
		if fs.Function == name {
			return fs
		}
	}
	return FunctionStat{Function: name}
}

func findNode(t *testing.T, s Snapshot, path []string) NodeStat {
	t.Helper()
	for _, n := range s.Nodes {
		if reflect.DeepEqual(n.Path, path) {
			return n
		}
	}
	t.Fatalf("node not found: %v; nodes=%v", path, s.Nodes)
	return NodeStat{}
}

// assertInvariants 校验三条守恒关系：
//  1. 函数自身值之和 == 总权重；
//  2. 节点总值 == 自身值 + 子节点总值之和；
//  3. 任一函数总值 <= 总权重。
func assertInvariants(t *testing.T, s Snapshot) {
	t.Helper()

	var selfSum int64
	for _, fs := range s.Functions {
		selfSum += fs.Self
		if fs.Total > s.TotalWeight {
			t.Fatalf("function %s total=%d exceeds total_weight=%d", fs.Function, fs.Total, s.TotalWeight)
		}
	}
	if selfSum != s.TotalWeight {
		t.Fatalf("function self sum=%d != total_weight=%d", selfSum, s.TotalWeight)
	}

	childTotal := map[string]int64{}
	for _, n := range s.Nodes {
		if len(n.Path) >= 2 {
			parent := strings.Join(n.Path[:len(n.Path)-1], "\x00")
			childTotal[parent] += n.Total
		}
	}
	for _, n := range s.Nodes {
		key := strings.Join(n.Path, "\x00")
		if got, want := n.Total, n.Self+childTotal[key]; got != want {
			t.Fatalf("node %v total=%d != self=%d + children_total=%d", n.Path, got, n.Self, want)
		}
	}
	var rootTotal int64
	for _, n := range s.Nodes {
		if len(n.Path) == 1 {
			rootTotal += n.Total
		}
	}
	if rootTotal != s.TotalWeight {
		t.Fatalf("root total sum=%d != total_weight=%d", rootTotal, s.TotalWeight)
	}
}

// 递归样本 A,F,G,F,H：F 在同一样本中出现两次，函数总值只计一次。
func TestRecursiveAttribution(t *testing.T) {
	agg := newLoggedAggregator(t, Config{})

	ok, reason := agg.Submit(Sample{Stack: []string{"A", "F", "G", "F", "H"}, Weight: 10})
	if !ok {
		t.Fatalf("sample rejected: %s", reason)
	}
	s := agg.Query()
	assertInvariants(t, s)

	f := findFunction(s, "F")
	if f.Self != 0 || f.Total != 10 {
		t.Fatalf("F attribution wrong: self=%d total=%d, want self=0 total=10", f.Self, f.Total)
	}
	h := findFunction(s, "H")
	if h.Self != 10 || h.Total != 10 {
		t.Fatalf("H attribution wrong: self=%d total=%d, want 10/10", h.Self, h.Total)
	}
	for _, name := range []string{"A", "G"} {
		fs := findFunction(s, name)
		if fs.Self != 0 || fs.Total != 10 {
			t.Fatalf("%s attribution wrong: self=%d total=%d, want 0/10", name, fs.Self, fs.Total)
		}
	}

	// 两个 F 是不同路径上的不同节点，互不影响。
	f1 := findNode(t, s, []string{"A", "F"})
	f2 := findNode(t, s, []string{"A", "F", "G", "F"})
	if f1.Total != 10 || f1.Self != 0 || f2.Total != 10 || f2.Self != 0 {
		t.Fatalf("node F stats wrong: %+v %+v", f1, f2)
	}
	leafH := findNode(t, s, []string{"A", "F", "G", "F", "H"})
	if leafH.Self != 10 || leafH.Total != 10 {
		t.Fatalf("leaf node H stats wrong: %+v", leafH)
	}

	// 全部总值 10 并列，按函数名升序。
	wantOrder := []string{"A", "F", "G", "H"}
	for i, want := range wantOrder {
		if s.Functions[i].Function != want {
			t.Fatalf("hotspot order[%d]=%s, want %s; full=%v", i, s.Functions[i].Function, want, s.Functions)
		}
	}
}

// 多条样本下的节点/函数守恒与排序。
func TestAggregationAndInvariants(t *testing.T) {
	agg := newLoggedAggregator(t, Config{})
	samples := []Sample{
		{Stack: []string{"A"}, Weight: 3},
		{Stack: []string{"A", "F"}, Weight: 5},
		{Stack: []string{"A", "F", "G", "F", "H"}, Weight: 10},
		{Stack: []string{"B", "F"}, Weight: 7},
	}
	for _, sm := range samples {
		if ok, reason := agg.Submit(sm); !ok {
			t.Fatalf("sample %v rejected: %s", sm.Stack, reason)
		}
	}
	s := agg.Query()
	assertInvariants(t, s)

	if s.TotalWeight != 25 {
		t.Fatalf("total weight=%d, want 25", s.TotalWeight)
	}
	f := findFunction(s, "F")
	if f.Total != 22 || f.Self != 12 {
		t.Fatalf("F self=%d total=%d, want 12/22", f.Self, f.Total)
	}
	a := findNode(t, s, []string{"A"})
	if a.Self != 3 || a.Total != 18 {
		t.Fatalf("node A self=%d total=%d, want 3/18", a.Self, a.Total)
	}
	// F 总值 22 最高，A 18 次之。
	if s.Functions[0].Function != "F" || s.Functions[0].Total != 22 {
		t.Fatalf("top hotspot=%+v, want F/22", s.Functions[0])
	}
	if s.Functions[1].Function != "A" || s.Functions[1].Total != 18 {
		t.Fatalf("second hotspot=%+v, want A/18", s.Functions[1])
	}
}

// 各类非法样本按原因分别计数，且不改变任何统计。
func TestInvalidSamples(t *testing.T) {
	agg := newLoggedAggregator(t, Config{MaxDepth: 3, MaxNodes: 1000})

	invalid := []struct {
		sample Sample
		reason RejectReason
	}{
		{Sample{Stack: nil, Weight: 1}, ReasonEmptyStack},
		{Sample{Stack: []string{}, Weight: 1}, ReasonEmptyStack},
		{Sample{Stack: []string{"A", ""}, Weight: 1}, ReasonEmptyName},
		{Sample{Stack: []string{"A"}, Weight: 0}, ReasonInvalidWeight},
		{Sample{Stack: []string{"A"}, Weight: -4}, ReasonInvalidWeight},
		{Sample{Stack: []string{"A", "B", "C", "D"}, Weight: 1}, ReasonDepthLimit},
	}
	for _, tc := range invalid {
		ok, reason := agg.Submit(tc.sample)
		if ok || reason != tc.reason {
			t.Fatalf("sample=%+v got accepted=%v reason=%s, want reason=%s",
				tc.sample, ok, reason, tc.reason)
		}
	}

	s := agg.Query()
	if s.TotalWeight != 0 || len(s.Nodes) != 0 || len(s.Functions) != 0 {
		t.Fatalf("rejected samples changed stats: %+v", s)
	}
	wantRejected := map[RejectReason]int64{
		ReasonEmptyStack:    2,
		ReasonEmptyName:     1,
		ReasonInvalidWeight: 2,
		ReasonDepthLimit:    1,
	}
	if !reflect.DeepEqual(s.Rejected, wantRejected) {
		t.Fatalf("rejected counters=%v, want %v", s.Rejected, wantRejected)
	}

	// 拒绝归类优先级确定性：空名优先于权重/深度。
	if ok, reason := agg.Submit(Sample{Stack: []string{"", "", "", ""}, Weight: 0}); ok || reason != ReasonEmptyName {
		t.Fatalf("precedence got accepted=%v reason=%s, want %s", ok, reason, ReasonEmptyName)
	}
}

// 节点上限：需要新建节点的样本整体拒绝且不留残节点；只走已有路径的样本仍接受。
func TestNodeLimit(t *testing.T) {
	agg := newLoggedAggregator(t, Config{MaxNodes: 3})

	if ok, reason := agg.Submit(Sample{Stack: []string{"A", "F", "G"}, Weight: 1}); !ok {
		t.Fatalf("seed sample rejected: %s", reason)
	}
	ok, reason := agg.Submit(Sample{Stack: []string{"A", "F", "G", "H"}, Weight: 2})
	if ok || reason != ReasonNodeLimit {
		t.Fatalf("node-limit sample got accepted=%v reason=%s", ok, reason)
	}
	if ok, reason := agg.Submit(Sample{Stack: []string{"B"}, Weight: 9}); ok {
		t.Fatalf("new-root sample accepted under node limit: %s", reason)
	}
	if ok, reason := agg.Submit(Sample{Stack: []string{"A", "F", "G"}, Weight: 4}); !ok {
		t.Fatalf("existing-path sample rejected: %s", reason)
	}

	s := agg.Query()
	assertInvariants(t, s)
	if len(s.Nodes) != 3 {
		t.Fatalf("node count=%d, want 3 (no residue)", len(s.Nodes))
	}
	leaf := findNode(t, s, []string{"A", "F", "G"})
	if leaf.Self != 5 || leaf.Total != 5 {
		t.Fatalf("leaf stats self=%d total=%d, want 5/5", leaf.Self, leaf.Total)
	}
	if s.TotalWeight != 5 || s.Rejected[ReasonNodeLimit] != 2 {
		t.Fatalf("total=%d rejected=%v, want total=5 node_limit=2", s.TotalWeight, s.Rejected)
	}
}

// 并发乱序提交的结果必须与串行提交相同；并发查询每次都得到守恒快照。
func TestConcurrentSubmission(t *testing.T) {
	samples := []Sample{
		{Stack: []string{"A", "F", "G", "F", "H"}, Weight: 10},
		{Stack: []string{"A", "F"}, Weight: 5},
		{Stack: []string{"B", "F"}, Weight: 7},
		{Stack: []string{"A"}, Weight: 3},
		{Stack: []string{}, Weight: 1},
		{Stack: []string{"A", ""}, Weight: 1},
		{Stack: []string{"A"}, Weight: 0},
	}

	// 串行参考结果；统计只依赖样本多重集，与提交顺序无关。
	serial := NewWithConfig(Config{})
	for _, sm := range samples {
		serial.Submit(sm)
	}
	want := serial.Query()

	agg := newLoggedAggregator(t, Config{})
	const goroutines = 16
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := range samples {
				agg.Submit(samples[(i+seed)%len(samples)])
			}
		}(g)
	}

	stop := make(chan struct{})
	queryDone := make(chan struct{})
	go func() {
		defer close(queryDone)
		for {
			select {
			case <-stop:
				return
			default:
				assertInvariants(t, agg.Query())
			}
		}
	}()
	wg.Wait()
	close(stop)
	<-queryDone

	got := agg.Query()
	assertInvariants(t, got)

	scale := int64(goroutines)
	if got.TotalWeight != want.TotalWeight*scale {
		t.Fatalf("concurrent total=%d, want %d", got.TotalWeight, want.TotalWeight*scale)
	}
	for _, wf := range want.Functions {
		gf := findFunction(got, wf.Function)
		if gf.Self != wf.Self*scale || gf.Total != wf.Total*scale {
			t.Fatalf("function %s concurrent=%+v, want self=%d total=%d",
				wf.Function, gf, wf.Self*scale, wf.Total*scale)
		}
	}
	if len(got.Nodes) != len(want.Nodes) {
		t.Fatalf("concurrent node count=%d, want %d", len(got.Nodes), len(want.Nodes))
	}
	for reason, wantCount := range want.Rejected {
		if got.Rejected[reason] != wantCount*scale {
			t.Fatalf("rejected[%s]=%d, want %d", reason, got.Rejected[reason], wantCount*scale)
		}
	}

	// 再以另一种乱序顺序提交一遍，验证与串行顺序无关的可复现性。
	other := NewWithConfig(Config{})
	for i := len(samples) - 1; i >= 0; i-- {
		other.Submit(samples[i])
	}
	want2 := other.Query()
	if !reflect.DeepEqual(want2.Functions, want.Functions) ||
		want2.TotalWeight != want.TotalWeight ||
		!reflect.DeepEqual(want2.Rejected, want.Rejected) {
		t.Fatalf("reverse-order result differs: %+v vs %+v", want2, want)
	}
}
