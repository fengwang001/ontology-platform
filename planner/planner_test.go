package planner

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func mustBuild(t *testing.T, tasks []int, edges []Edge) *Planner {
	t.Helper()
	p, err := Build(tasks, edges)
	if err != nil {
		t.Fatalf("建图失败: %v", err)
	}
	return p
}

func mustComplete(t *testing.T, p *Planner, ids ...int) {
	t.Helper()
	for _, id := range ids {
		if err := p.ReportComplete(id); err != nil {
			t.Fatalf("完成上报任务 %d 失败: %v", id, err)
		}
	}
}

func mustLoss(t *testing.T, p *Planner, from, to int) {
	t.Helper()
	if err := p.ReportLoss(from, to); err != nil {
		t.Fatalf("丢失上报边 %d->%d 失败: %v", from, to, err)
	}
}

func mustFail(t *testing.T, p *Planner, id int) (regions, tasks []int) {
	t.Helper()
	regions, tasks, err := p.ReportFail(id)
	if err != nil {
		t.Fatalf("失败上报任务 %d 失败: %v", id, err)
	}
	return regions, tasks
}

// 标准测试图：
//
//	区域 A={1,2}（流水线 1-2），区域 B={3,4}（流水线 3-4），
//	区域 C={5} D={6} E={7} F={8}
//	阻塞边：1->3, 4->5, 5->6, 7->6, 6->8
func standardGraph() ([]int, []Edge) {
	tasks := []int{1, 2, 3, 4, 5, 6, 7, 8}
	edges := []Edge{
		{1, 2, Pipeline},
		{3, 4, Pipeline},
		{1, 3, Blocking},
		{4, 5, Blocking},
		{5, 6, Blocking},
		{7, 6, Blocking},
		{6, 8, Blocking},
	}
	return tasks, edges
}

func TestRegionDivision(t *testing.T) {
	tasks, edges := standardGraph()
	p := mustBuild(t, tasks, edges)

	want := map[int]int{1: 1, 2: 1, 3: 3, 4: 3, 5: 5, 6: 6, 7: 7, 8: 8}
	for id, wantRegion := range want {
		got, ok := p.RegionOf(id)
		if !ok {
			t.Fatalf("任务 %d 无区域", id)
		}
		t.Logf("输入=任务%d 输出=区域%d 依据=流水线边连通分量取最小编号，期望%d", id, got, wantRegion)
		if got != wantRegion {
			t.Errorf("任务 %d 区域 = %d，期望 %d", id, got, wantRegion)
		}
	}
	if _, ok := p.RegionOf(99); ok {
		t.Errorf("不存在的任务 99 不应有区域")
	}
}

// 阻塞结果丢失使已完成的上游区域被拉入重启集合（规则 3），
// 且下游消费区域连带重启（规则 2），多层级联迭代收敛。
func TestFailPullsCompletedUpstreamOnLoss(t *testing.T) {
	tasks, edges := standardGraph()
	p := mustBuild(t, tasks, edges)

	mustComplete(t, p, 1, 2) // 区域 A 已完成
	mustLoss(t, p, 1, 3)     // A 产出的阻塞结果丢失

	regions, gotTasks := mustFail(t, p, 3)
	wantRegions := []int{1, 3, 5, 6, 8}
	wantTasks := []int{1, 2, 3, 4, 5, 6, 8}
	t.Logf("输入=失败任务3（区域A已完成且结果1->3丢失） 输出=区域%v 任务%v", regions, gotTasks)
	t.Logf("依据=规则1含区域3；规则2连带消费区域5,6,8；规则3结果1->3不可用且生产者已完成，拉入区域1；规则3边7->6生产者7仍在运行，不拉入区域7")
	if !reflect.DeepEqual(regions, wantRegions) {
		t.Errorf("重启区域 = %v，期望 %v", regions, wantRegions)
	}
	if !reflect.DeepEqual(gotTasks, wantTasks) {
		t.Errorf("重启任务 = %v，期望 %v", gotTasks, wantTasks)
	}

	states, lost := p.Snapshot()
	for _, id := range wantTasks {
		if states[id] != Running {
			t.Errorf("任务 %d 应为运行中，实际 %s", id, states[id])
		}
	}
	if states[7] != Running {
		t.Errorf("无关区域任务 7 不应受影响，实际 %s", states[7])
	}
	t.Logf("上报后状态=%v 丢失边=%v 依据=集合内任务全部运行中，所产结果丢失标记清除", states, lost)
	if len(lost) != 0 {
		t.Errorf("区域 1 在集合内，其所产结果 1->3 的丢失标记应已清除，实际丢失边 %v", lost)
	}
}

// 上游生产者仍在运行时，即使结果不可用也不被拉入（规则 3 但书）。
func TestFailSkipsRunningProducer(t *testing.T) {
	tasks, edges := standardGraph()
	p := mustBuild(t, tasks, edges)

	regions, gotTasks := mustFail(t, p, 3)
	wantRegions := []int{3, 5, 6, 8}
	wantTasks := []int{3, 4, 5, 6, 8}
	t.Logf("输入=失败任务3（生产者1仍在运行） 输出=区域%v 任务%v", regions, gotTasks)
	t.Logf("依据=规则3结果1->3不可用但生产者1仍在运行，不拉入区域1；规则2连带5,6,8；区域7与失败无关不重启")
	if !reflect.DeepEqual(regions, wantRegions) {
		t.Errorf("重启区域 = %v，期望 %v", regions, wantRegions)
	}
	if !reflect.DeepEqual(gotTasks, wantTasks) {
		t.Errorf("重启任务 = %v，期望 %v", gotTasks, wantTasks)
	}
}

// 多层级联：沿阻塞链反向逐级拉入已完成且结果丢失的上游，迭代至收敛。
func TestFailCascadeConverges(t *testing.T) {
	tasks := []int{1, 2, 3, 4}
	edges := []Edge{
		{1, 2, Blocking},
		{2, 3, Blocking},
		{3, 4, Blocking},
	}
	p := mustBuild(t, tasks, edges)

	mustComplete(t, p, 1, 2, 3)
	mustLoss(t, p, 1, 2)
	mustLoss(t, p, 2, 3)
	mustLoss(t, p, 3, 4)

	regions, gotTasks := mustFail(t, p, 4)
	want := []int{1, 2, 3, 4}
	t.Logf("输入=失败任务4（链上三级结果均丢失且生产者已完成） 输出=区域%v 任务%v", regions, gotTasks)
	t.Logf("依据=规则3逐级拉入：4<-3<-2<-1，迭代至不再变化")
	if !reflect.DeepEqual(regions, want) || !reflect.DeepEqual(gotTasks, want) {
		t.Errorf("重启集合 = 区域%v 任务%v，期望均为 %v", regions, gotTasks, want)
	}
}

// 下游消费区域连带重启后，再失败时丢失标记已清除、状态已重置。
func TestFailClearsLostAndResetsState(t *testing.T) {
	tasks, edges := standardGraph()
	p := mustBuild(t, tasks, edges)

	mustComplete(t, p, 1, 2, 3, 4)
	mustLoss(t, p, 1, 3)
	mustFail(t, p, 5) // 重启集合含 5,6,8；规则3：4->5 可用、7->6 生产者运行中，不拉入

	states, lost := p.Snapshot()
	t.Logf("输入=失败任务5 输出=状态%v 丢失边%v", states, lost)
	if states[1] != Completed || states[3] != Completed {
		t.Errorf("区域 1,3 与失败无关应保持已完成，实际 1=%s 3=%s", states[1], states[3])
	}
	if len(lost) != 1 || lost[0] != (Edge{1, 3, Blocking}) {
		t.Errorf("丢失边应只剩 1->3，实际 %v", lost)
	}
}

func TestBuildErrors(t *testing.T) {
	cases := []struct {
		name  string
		tasks []int
		edges []Edge
		kind  ErrKind
	}{
		{"边引用不存在任务", []int{1}, []Edge{{1, 2, Pipeline}}, ErrTaskNotFound},
		{"重复边", []int{1, 2}, []Edge{{1, 2, Pipeline}, {1, 2, Blocking}}, ErrDuplicateEdge},
		{"阻塞边同区域", []int{1, 2}, []Edge{{1, 2, Pipeline}, {2, 1, Blocking}}, ErrBlockingSameRegion},
		{"区域成环", []int{1, 2}, []Edge{{1, 2, Blocking}, {2, 1, Blocking}}, ErrRegionCycle},
		// 多因并存：任务不存在 + 重复边，只报任务不存在。
		{"优先报任务不存在", []int{1}, []Edge{{1, 9, Pipeline}, {1, 9, Pipeline}}, ErrTaskNotFound},
		// 多因并存：同区域（阻塞边 2->1）+ 区域 {1,2} 与 {3} 成环，只报同区域。
		{"优先报同区域", []int{1, 2, 3}, []Edge{
			{1, 2, Pipeline}, {2, 1, Blocking}, {1, 3, Blocking}, {3, 2, Blocking},
		}, ErrBlockingSameRegion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Build(tc.tasks, tc.edges)
			t.Logf("输入=任务%v 边%v 输出=%v 依据=建图校验按序只报第一个原因", tc.tasks, tc.edges, err)
			if !IsKind(err, tc.kind) {
				t.Fatalf("错误类型 = %v，期望 kind=%d", err, tc.kind)
			}
		})
	}
}

func TestReportErrors(t *testing.T) {
	tasks, edges := standardGraph()
	p := mustBuild(t, tasks, edges)
	before, beforeLost := p.Snapshot()

	check := func(name string, err error, kind ErrKind) {
		t.Helper()
		t.Logf("输入=%s 输出=%v", name, err)
		if !IsKind(err, kind) {
			t.Errorf("%s: 错误 = %v，期望 kind=%d", name, err, kind)
		}
	}

	check("完成不存在任务99", p.ReportComplete(99), ErrTaskNotFound)
	check("失败不存在任务99", func() error { _, _, err := p.ReportFail(99); return err }(), ErrTaskNotFound)
	mustComplete(t, p, 7)
	check("重复完成任务7", p.ReportComplete(7), ErrTaskNotRunning)
	check("失败已完成任务7", func() error { _, _, err := p.ReportFail(7); return err }(), ErrTaskNotRunning)
	check("丢失边引用不存在任务", p.ReportLoss(1, 99), ErrTaskNotFound)
	check("丢失不存在的边", p.ReportLoss(1, 5), ErrEdgeNotFound)
	check("丢失非阻塞边", p.ReportLoss(1, 2), ErrNotBlockingEdge)
	check("结果尚未产出", p.ReportLoss(1, 3), ErrResultNotProduced)

	// 被拒绝的操作不得改变任何状态（任务 7 的完成上报除外，先记录再比对）。
	after, afterLost := p.Snapshot()
	wantStates := map[int]TaskState{}
	for id, s := range before {
		wantStates[id] = s
	}
	wantStates[7] = Completed
	if !reflect.DeepEqual(after, wantStates) || !reflect.DeepEqual(afterLost, beforeLost) {
		t.Errorf("被拒绝的操作改变了状态: 状态%v 丢失边%v", after, afterLost)
	}
}

// 已丢失的再报视为成功且无变化。
func TestLossIdempotent(t *testing.T) {
	tasks, edges := standardGraph()
	p := mustBuild(t, tasks, edges)
	mustComplete(t, p, 1)
	mustLoss(t, p, 1, 3)
	_, lostBefore := p.Snapshot()

	if err := p.ReportLoss(1, 3); err != nil {
		t.Fatalf("重复丢失上报应成功: %v", err)
	}
	_, lostAfter := p.Snapshot()
	t.Logf("输入=重复丢失上报1->3 输出=成功 依据=已丢失再报无变化，丢失边%v", lostAfter)
	if !reflect.DeepEqual(lostBefore, lostAfter) {
		t.Errorf("重复上报后丢失边变化: %v -> %v", lostBefore, lostAfter)
	}
}

// 并发上报与查询：效果等价于某个串行顺序，且相同上报序列结果完全相同。
func TestConcurrentReportsDeterministic(t *testing.T) {
	tasks, edges := standardGraph()

	run := func() (map[int]TaskState, []Edge) {
		p := mustBuild(t, tasks, edges)
		var wg sync.WaitGroup
		for _, id := range []int{1, 2, 4, 5, 6, 7, 8} {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				_ = p.ReportComplete(id)
			}(id)
		}
		wg.Wait()
		// 串行部分：丢失 + 失败上报。
		mustLoss(t, p, 1, 3)
		mustLoss(t, p, 1, 3) // 幂等
		regions, gotTasks := mustFail(t, p, 3)
		t.Logf("输入=并发完成后丢失1->3再失败3 输出=区域%v 任务%v", regions, gotTasks)
		var wg2 sync.WaitGroup
		for i := 0; i < 16; i++ {
			wg2.Add(1)
			go func() {
				defer wg2.Done()
				p.Snapshot()
				p.RegionOf(1 + i%8)
			}()
		}
		wg2.Wait()
		return p.Snapshot()
	}

	states1, lost1 := run()
	states2, lost2 := run()
	if !reflect.DeepEqual(states1, states2) || !reflect.DeepEqual(lost1, lost2) {
		t.Errorf("相同上报序列结果不同: (%v,%v) vs (%v,%v)", states1, lost1, states2, lost2)
	}
	// 全部任务完成上报后，失败 3 重启集合 = {1,3,5,6,8}，7 保持已完成。
	if states1[7] != Completed {
		t.Errorf("无关任务 7 应保持已完成，实际 %s", states1[7])
	}
	for _, id := range []int{1, 2, 3, 4, 5, 6, 8} {
		if states1[id] != Running {
			t.Errorf("任务 %d 应被重启为运行中，实际 %s", id, states1[id])
		}
	}
}

// 失败上报后丢失标记清除，结果重新可用，再次失败不再拉入上游。
func TestRefailAfterRestart(t *testing.T) {
	tasks, edges := standardGraph()
	p := mustBuild(t, tasks, edges)

	mustComplete(t, p, 1, 2)
	mustLoss(t, p, 1, 3)
	mustFail(t, p, 3) // 重启 {1,3,5,6,8}，丢失标记清除

	mustComplete(t, p, 1, 2) // 区域 1 重新完成，结果 1->3 重新可用
	regions, _ := mustFail(t, p, 3)
	want := []int{3, 5, 6, 8}
	t.Logf("输入=重启后再次失败3 输出=区域%v 依据=结果1->3已重新产出且未丢失，规则3不拉入区域1", regions)
	if !reflect.DeepEqual(regions, want) {
		t.Errorf("重启区域 = %v，期望 %v", regions, want)
	}
}

func ExamplePlanner_ReportFail() {
	p, _ := Build([]int{1, 2, 3}, []Edge{{1, 2, Blocking}, {2, 3, Blocking}})
	_ = p.ReportComplete(1)
	_ = p.ReportComplete(2)
	_ = p.ReportLoss(1, 2)
	_ = p.ReportLoss(2, 3)
	regions, tasks, _ := p.ReportFail(3)
	fmt.Println(regions, tasks)
	// Output: [1 2 3] [1 2 3]
}
