package hotspot

import (
	"bytes"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

func mustAccept(t *testing.T, a *Aggregator, s StackSample) {
	t.Helper()
	if ok, reason := a.Submit(s); !ok {
		t.Fatalf("sample %v weight %d rejected: %s", s.Stack, s.Weight, reason)
	}
}

func assertReject(t *testing.T, a *Aggregator, s StackSample, want RejectReason) {
	t.Helper()
	if ok, reason := a.Submit(s); ok {
		t.Fatalf("sample %v weight %d accepted, want reject %s", s.Stack, s.Weight, want)
	} else if reason != want {
		t.Fatalf("reject reason = %s, want %s", reason, want)
	}
}

func findFn(snap Snapshot, name string) FunctionStat {
	for _, f := range snap.Functions {
		if f.Name == name {
			return f
		}
	}
	return FunctionStat{Name: name}
}

func findNode(snap Snapshot, path ...string) NodeStat {
	want := strings.Join(path, "\x00")
	for _, n := range snap.Nodes {
		if strings.Join(n.Path, "\x00") == want {
			return n
		}
	}
	return NodeStat{Path: path}
}

// verifyInvariants 检查题目要求的全部守恒恒等式与热点排序。
func verifyInvariants(t *testing.T, snap Snapshot) {
	t.Helper()

	var funcSelfSum int64
	for _, f := range snap.Functions {
		if f.Total > snap.TotalWeight {
			t.Errorf("function %s total %d exceeds total weight %d", f.Name, f.Total, snap.TotalWeight)
		}
		funcSelfSum += f.Self
	}
	if funcSelfSum != snap.TotalWeight {
		t.Errorf("sum of function self = %d, want total weight %d", funcSelfSum, snap.TotalWeight)
	}

	byPath := make(map[string]NodeStat, len(snap.Nodes))
	var nodeSelfSum int64
	for _, n := range snap.Nodes {
		byPath[strings.Join(n.Path, "\x00")] = n
		nodeSelfSum += n.Self
	}
	if nodeSelfSum != snap.TotalWeight {
		t.Errorf("sum of node self = %d, want total weight %d", nodeSelfSum, snap.TotalWeight)
	}

	for _, n := range snap.Nodes {
		var childTotalSum int64
		for _, childPath := range n.Children {
			child, ok := byPath[strings.Join(childPath, "\x00")]
			if !ok {
				t.Fatalf("node %v references missing child %v", n.Path, childPath)
			}
			childTotalSum += child.Total
		}
		if n.Total != n.Self+childTotalSum {
			t.Errorf("node %v total = %d, want self %d + children total %d",
				n.Path, n.Total, n.Self, childTotalSum)
		}
	}

	for i := 1; i < len(snap.HotFunctions); i++ {
		prev, cur := snap.HotFunctions[i-1], snap.HotFunctions[i]
		if prev.Total < cur.Total || (prev.Total == cur.Total && prev.Name > cur.Name) {
			t.Errorf("hot list not sorted at %d: %+v before %+v", i, prev, cur)
		}
	}
}

// TestRecursiveAttribution 使用根到叶 A,F,G,F,H 的递归样本验证 F 的归因：
// F 在同一样本内出现两次，函数总值只计一次权重；两个 F 对应不同路径节点。
func TestRecursiveAttribution(t *testing.T) {
	a := New(Config{})
	mustAccept(t, a, StackSample{Stack: []string{"A", "F", "G", "F", "H"}, Weight: 10})

	snap := a.Query()
	if snap.TotalWeight != 10 || snap.AcceptedSamples != 1 {
		t.Fatalf("total weight = %d, accepted = %d", snap.TotalWeight, snap.AcceptedSamples)
	}

	if got := findFn(snap, "F"); got.Self != 0 || got.Total != 10 {
		t.Errorf("F attribution = self %d total %d, want self 0 total 10", got.Self, got.Total)
	}
	if got := findFn(snap, "H"); got.Self != 10 || got.Total != 10 {
		t.Errorf("H attribution = self %d total %d, want 10/10", got.Self, got.Total)
	}
	if got := findFn(snap, "A"); got.Self != 0 || got.Total != 10 {
		t.Errorf("A attribution = self %d total %d, want 0/10", got.Self, got.Total)
	}

	outerF := findNode(snap, "A", "F")
	if outerF.Total != 10 || outerF.Self != 0 {
		t.Errorf("outer F node total/self = %d/%d, want 10/0", outerF.Total, outerF.Self)
	}
	innerF := findNode(snap, "A", "F", "G", "F")
	if innerF.Total != 10 || innerF.Self != 0 {
		t.Errorf("inner F node total/self = %d/%d, want 10/0", innerF.Total, innerF.Self)
	}
	hNode := findNode(snap, "A", "F", "G", "F", "H")
	if hNode.Self != 10 || hNode.Total != 10 {
		t.Errorf("H node self/total = %d/%d, want 10/10", hNode.Self, hNode.Total)
	}
	if snap.NodeCount != 5 {
		t.Errorf("node count = %d, want 5 (recursive F yields two path nodes)", snap.NodeCount)
	}
	if snap.FunctionCount != 4 {
		t.Errorf("function count = %d, want 4 (F deduplicated by name)", snap.FunctionCount)
	}
	verifyInvariants(t, snap)
}

// TestConcurrentSubmission 验证并发提交后的结果与任意顺序串行提交相同，
// 且与提交并发发生的查询始终返回满足恒等式的一致快照。
func TestConcurrentSubmission(t *testing.T) {
	baseSamples := []StackSample{
		{Stack: []string{"A", "F", "G", "F", "H"}, Weight: 3},
		{Stack: []string{"A", "F", "H"}, Weight: 7},
		{Stack: []string{"B", "C"}, Weight: 5},
		{Stack: []string{"R", "R", "R"}, Weight: 9},
		{Stack: []string{"A", "F", "G", "F", "H"}, Weight: 4},
		{Stack: []string{"B"}, Weight: 2},
	}

	serial := New(Config{})
	for _, s := range baseSamples {
		mustAccept(t, serial, s)
	}
	want := serial.Query()

	var wg sync.WaitGroup
	for round := 0; round < 8; round++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			c := New(Config{})
			rng := rand.New(rand.NewSource(seed))

			var inner sync.WaitGroup
			for _, idx := range rng.Perm(len(baseSamples)) {
				inner.Add(1)
				go func(s StackSample) {
					defer inner.Done()
					if ok, reason := c.Submit(s); !ok {
						t.Errorf("unexpected reject %s", reason)
					}
				}(baseSamples[idx])
			}

			// 提交进行中并发查询，中间快照也必须自洽。
			for reader := 0; reader < 3; reader++ {
				inner.Add(1)
				go func() {
					defer inner.Done()
					verifyInvariants(t, c.Query())
				}()
			}

			inner.Wait()
			got := c.Query()
			if got.TotalWeight != want.TotalWeight ||
				got.AcceptedSamples != want.AcceptedSamples ||
				got.NodeCount != want.NodeCount ||
				got.FunctionCount != want.FunctionCount {
				t.Errorf("concurrent result %+v differs from serial %+v", got, want)
			}
			for _, f := range want.Functions {
				if g := findFn(got, f.Name); g != f {
					t.Errorf("function %s concurrent = %+v, want %+v", f.Name, g, f)
				}
			}
			verifyInvariants(t, got)
		}(int64(round + 1))
	}
	wg.Wait()
}

// TestDecisionLogging 验证日志中包含输入、输出（accept/reject）与判定依据。
func TestDecisionLogging(t *testing.T) {
	var buf bytes.Buffer
	// 5 个节点正好容纳 A,F,G,F,H 路径；之后任何需要建新节点的样本都被拒绝。
	a := New(Config{MaxNodes: 5}, WithLogger(&buf))

	mustAccept(t, a, StackSample{Stack: []string{"A", "F", "G", "F", "H"}, Weight: 6})
	assertReject(t, a, StackSample{Stack: []string{"A", "X"}, Weight: 1}, ReasonNodeLimitExceeded)
	assertReject(t, a, StackSample{Stack: []string{}, Weight: 1}, ReasonEmptyStack)
	assertReject(t, a, StackSample{Stack: []string{"A"}, Weight: 0}, ReasonInvalidWeight)
	_ = a.Query()

	log := buf.String()
	for _, want := range []string{
		`msg="sample accepted"`,
		"[A F G F H]",
		"weight=6",
		"decision=accept",
		"basis=",
		`msg="sample rejected"`,
		"reason=node_limit_exceeded",
		"reason=empty_stack",
		"reason=invalid_weight",
		`msg="query snapshot"`,
		"hot_functions=",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q\nfull log:\n%s", want, log)
		}
	}
}

func TestInvalidSamples(t *testing.T) {
	a := New(Config{MaxDepth: 2})

	assertReject(t, a, StackSample{Stack: nil, Weight: 1}, ReasonEmptyStack)
	assertReject(t, a, StackSample{Stack: []string{}, Weight: 1}, ReasonEmptyStack)
	assertReject(t, a, StackSample{Stack: []string{"A", ""}, Weight: 1}, ReasonEmptyFunction)
	assertReject(t, a, StackSample{Stack: []string{""}, Weight: 1}, ReasonEmptyFunction)
	assertReject(t, a, StackSample{Stack: []string{"A"}, Weight: 0}, ReasonInvalidWeight)
	assertReject(t, a, StackSample{Stack: []string{"A"}, Weight: -3}, ReasonInvalidWeight)
	assertReject(t, a, StackSample{Stack: []string{"A", "B", "C"}, Weight: 1}, ReasonDepthExceeded)

	snap := a.Query()
	if snap.TotalWeight != 0 || snap.AcceptedSamples != 0 || snap.RejectedSamples != 7 {
		t.Fatalf("totals = weight %d accepted %d rejected %d, want 0/0/7",
			snap.TotalWeight, snap.AcceptedSamples, snap.RejectedSamples)
	}
	if snap.NodeCount != 0 || snap.FunctionCount != 0 {
		t.Fatalf("rejected samples left state behind: nodes %d funcs %d", snap.NodeCount, snap.FunctionCount)
	}
	wantReasons := map[RejectReason]int64{
		ReasonEmptyStack:    2,
		ReasonEmptyFunction: 2,
		ReasonInvalidWeight: 2,
		ReasonDepthExceeded: 1,
	}
	for reason, want := range wantReasons {
		if got := snap.RejectReasons[reason]; got != want {
			t.Errorf("reject reason %s count = %d, want %d", reason, got, want)
		}
	}
	verifyInvariants(t, snap)
}

func TestNodeLimit(t *testing.T) {
	// 上限 3 个节点：A、A/B、A/B/C 占满。
	a := New(Config{MaxNodes: 3})
	mustAccept(t, a, StackSample{Stack: []string{"A", "B", "C"}, Weight: 1})

	// 需要新建节点的样本整体拒绝，且不留下任何新节点。
	assertReject(t, a, StackSample{Stack: []string{"A", "B", "D"}, Weight: 1}, ReasonNodeLimitExceeded)
	assertReject(t, a, StackSample{Stack: []string{"X"}, Weight: 1}, ReasonNodeLimitExceeded)

	if before := a.Query(); before.NodeCount != 3 {
		t.Fatalf("node count = %d, want 3", before.NodeCount)
	}

	// 只经过已有路径的样本仍被接受，包括在已有中间节点处结束的新叶子。
	mustAccept(t, a, StackSample{Stack: []string{"A", "B"}, Weight: 5})
	mustAccept(t, a, StackSample{Stack: []string{"A", "B", "C"}, Weight: 2})

	snap := a.Query()
	if snap.NodeCount != 3 {
		t.Fatalf("node count = %d, still want 3", snap.NodeCount)
	}
	if snap.TotalWeight != 8 {
		t.Fatalf("total weight = %d, want 8", snap.TotalWeight)
	}
	if n := findNode(snap, "A", "B"); n.Self != 5 || n.Total != 8 {
		t.Errorf("node A/B self/total = %d/%d, want 5/8", n.Self, n.Total)
	}
	if n := findNode(snap, "A", "B", "C"); n.Self != 3 || n.Total != 3 {
		t.Errorf("node A/B/C self/total = %d/%d, want 3/3", n.Self, n.Total)
	}
	if snap.RejectReasons[ReasonNodeLimitExceeded] != 2 {
		t.Errorf("node-limit rejects = %d, want 2", snap.RejectReasons[ReasonNodeLimitExceeded])
	}
	verifyInvariants(t, snap)
}

func TestInvariantsAcrossMixedSamples(t *testing.T) {
	a := New(Config{MaxDepth: 6})
	samples := []StackSample{
		{Stack: []string{"A", "F", "G", "F", "H"}, Weight: 3},
		{Stack: []string{"A", "F", "H"}, Weight: 7},
		{Stack: []string{"A", "F", "G", "F", "H"}, Weight: 2},
		{Stack: []string{"B", "C"}, Weight: 5},
		{Stack: []string{"B"}, Weight: 4},
		{Stack: []string{"R", "R", "R"}, Weight: 9},
	}
	for _, s := range samples {
		mustAccept(t, a, s)
	}
	snap := a.Query()

	if snap.TotalWeight != 30 {
		t.Fatalf("total weight = %d, want 30", snap.TotalWeight)
	}
	if got := findFn(snap, "F"); got.Total != 12 || got.Self != 0 {
		t.Errorf("F = %+v, want total 12 self 0 (recurrence counted once)", got)
	}
	if got := findFn(snap, "H"); got.Total != 12 || got.Self != 12 {
		t.Errorf("H = %+v, want total 12 self 12", got)
	}
	if got := findFn(snap, "B"); got.Total != 9 || got.Self != 4 {
		t.Errorf("B = %+v, want total 9 self 4", got)
	}
	if got := findFn(snap, "R"); got.Total != 9 || got.Self != 9 {
		t.Errorf("R = %+v, want total 9 self 9 (R,R,R counted once for total)", got)
	}

	hot := snap.HotFunctions
	if hot[0].Name != "A" || hot[0].Total != 12 {
		t.Errorf("hottest = %+v, want A total 12", hot[0])
	}
	// F 与 H 总值并列 12，按函数名升序 F 应在 H 前。
	if hot[1].Name != "F" || hot[2].Name != "H" {
		t.Errorf("tie order = %s,%s, want F,H", hot[1].Name, hot[2].Name)
	}
	verifyInvariants(t, snap)
}
