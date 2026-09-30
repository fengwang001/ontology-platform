package pipeline

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustBuild(t *testing.T, tasks []int, edges []Edge, logBuf *bytes.Buffer) *Planner {
	t.Helper()
	var opts []Option
	if logBuf != nil {
		opts = append(opts, WithLogger(logBuf))
	}
	p, err := New(tasks, edges, opts...)
	if err != nil {
		t.Fatalf("New returned unexpected error: %v", err)
	}
	return p
}

func TestRegionPartitionAndID(t *testing.T) {
	// 流水线边（忽略方向）连通：4-5 同区，编号取最小值 4；3 自成一区。
	p := mustBuild(t, []int{3, 4, 5}, []Edge{
		{From: 5, To: 4, Kind: Pipeline},
	}, nil)

	if got := p.Regions(); fmt.Sprint(got) != "[3 4]" {
		t.Fatalf("Regions = %v, want [3 4]", got)
	}
	if r, _ := p.RegionOf(5); r != 4 {
		t.Fatalf("RegionOf(5) = %d, want 4", r)
	}
	if r, _ := p.RegionOf(4); r != 4 {
		t.Fatalf("RegionOf(4) = %d, want 4", r)
	}
	members, err := p.RegionTasks(4)
	if err != nil || fmt.Sprint(members) != "[4 5]" {
		t.Fatalf("RegionTasks(4) = %v, %v; want [4 5]", members, err)
	}
	if _, err := p.RegionTasks(99); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("RegionTasks(99) err = %v, want ErrTaskNotFound", err)
	}
}

func TestBuildValidationReasonsAndPriority(t *testing.T) {
	cases := []struct {
		name  string
		tasks []int
		edges []Edge
		want  error
	}{
		{
			name:  "unknown endpoint",
			tasks: []int{1, 2},
			edges: []Edge{{From: 1, To: 9, Kind: Blocking}},
			want:  ErrUnknownTaskEndpoint,
		},
		{
			// 未知端点出现在重复边之前：先报未知端点（按边序逐条校验）。
			name:  "unknown beats duplicate",
			tasks: []int{1, 2},
			edges: []Edge{
				{From: 2, To: 9, Kind: Pipeline},
				{From: 1, To: 2, Kind: Pipeline},
				{From: 1, To: 2, Kind: Pipeline},
			},
			want: ErrUnknownTaskEndpoint,
		},
		{
			name:  "duplicate ordered pair",
			tasks: []int{1, 2},
			edges: []Edge{
				{From: 1, To: 2, Kind: Pipeline},
				{From: 1, To: 2, Kind: Blocking},
			},
			want: ErrDuplicateEdge,
		},
		{
			// 反向有序对不算重复，合并为同一区域，合法。
			name:  "reverse pair is distinct",
			tasks: []int{1, 2},
			edges: []Edge{
				{From: 1, To: 2, Kind: Pipeline},
				{From: 2, To: 1, Kind: Pipeline},
			},
			want: nil,
		},
		{
			// 重复最早出现，优先于同区阻塞与成环。
			name:  "duplicate beats intra and cycle",
			tasks: []int{1, 2, 3},
			edges: []Edge{
				{From: 1, To: 2, Kind: Blocking},
				{From: 1, To: 2, Kind: Blocking},
				{From: 3, To: 1, Kind: Blocking},
				{From: 2, To: 3, Kind: Blocking},
			},
			want: ErrDuplicateEdge,
		},
		{
			// 1、2 由流水线连通为一区，阻塞边 2->1 落在同区；
			// 同时区域间也有环，按序先报同区阻塞。
			name:  "intra beats cycle",
			tasks: []int{1, 2, 3},
			edges: []Edge{
				{From: 1, To: 2, Kind: Pipeline},
				{From: 2, To: 1, Kind: Blocking},
				{From: 3, To: 1, Kind: Blocking},
				{From: 1, To: 3, Kind: Blocking},
			},
			want: ErrIntraRegionBlock,
		},
		{
			name:  "region cycle",
			tasks: []int{1, 2},
			edges: []Edge{
				{From: 1, To: 2, Kind: Blocking},
				{From: 2, To: 1, Kind: Blocking},
			},
			want: ErrRegionCycle,
		},
		{
			name:  "three region cycle",
			tasks: []int{1, 2, 3},
			edges: []Edge{
				{From: 1, To: 2, Kind: Blocking},
				{From: 2, To: 3, Kind: Blocking},
				{From: 3, To: 1, Kind: Blocking},
			},
			want: ErrRegionCycle,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.tasks, tc.edges, WithLogger(nil))
			if tc.want == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// chainPlanner 构造区域链 A={1} -> B={2} -> C={3} -> D={4}，
// 另有无关区域 E={5,6}（流水线连通）。阻塞边 1->2、2->3、3->4。
func chainPlanner(t *testing.T, logBuf *bytes.Buffer) *Planner {
	t.Helper()
	return mustBuild(t, []int{1, 2, 3, 4, 5, 6}, []Edge{
		{From: 1, To: 2, Kind: Blocking},
		{From: 2, To: 3, Kind: Blocking},
		{From: 3, To: 4, Kind: Blocking},
		{From: 5, To: 6, Kind: Pipeline},
	}, logBuf)
}

func assertPlan(t *testing.T, got RestartPlan, wantRegions, wantTasks []int) {
	t.Helper()
	if fmt.Sprint(got.Regions) != fmt.Sprint(wantRegions) {
		t.Fatalf("regions = %v, want %v", got.Regions, wantRegions)
	}
	if fmt.Sprint(got.Tasks) != fmt.Sprint(wantTasks) {
		t.Fatalf("tasks = %v, want %v", got.Tasks, wantTasks)
	}
}

func TestFailBasicsAndUnrelatedRegionUntouched(t *testing.T) {
	p := chainPlanner(t, nil)

	if _, err := p.Fail(42); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("Fail(42) err = %v, want ErrTaskNotFound", err)
	}

	// 上游均在运行：fail D 只重启 D，结果未产出不拉入运行中的生产者，
	// 无关区域 E 不受影响。
	plan, err := p.Fail(4)
	if err != nil {
		t.Fatalf("Fail(4) err = %v", err)
	}
	assertPlan(t, plan, []int{4}, []int{4})

	// 已完成任务不能再上报失败或完成。
	if err := p.Complete(5); err != nil {
		t.Fatalf("Complete(5) err = %v", err)
	}
	if _, err := p.Fail(5); !errors.Is(err, ErrTaskNotRunning) {
		t.Fatalf("Fail(5) err = %v, want ErrTaskNotRunning", err)
	}
	if err := p.Complete(5); !errors.Is(err, ErrTaskNotRunning) {
		t.Fatalf("second Complete(5) err = %v, want ErrTaskNotRunning", err)
	}
}

func TestConsumerRegionCascade(t *testing.T) {
	p := chainPlanner(t, nil)

	// fail A：规则二沿 1->2->3->4 拉入全部下游消费区域，E 不参与。
	plan, err := p.Fail(1)
	if err != nil {
		t.Fatalf("Fail(1) err = %v", err)
	}
	assertPlan(t, plan, []int{1, 2, 3, 4}, []int{1, 2, 3, 4})
}

func TestLostResultPullsCompletedUpstream(t *testing.T) {
	p := chainPlanner(t, nil)

	// 未丢失时，生产者 1 已完成 -> 结果可用，A 不被拉入；
	// 规则二仍将下游消费区域 C、D 连带重启。
	if err := p.Complete(1); err != nil {
		t.Fatalf("Complete(1): %v", err)
	}
	avail, err := p.ResultAvailable(1, 2)
	if err != nil || !avail {
		t.Fatalf("ResultAvailable = %v, %v; want true", avail, err)
	}
	plan, err := p.RestartPlanFor(2)
	if err != nil {
		t.Fatalf("RestartPlanFor(2): %v", err)
	}
	assertPlan(t, plan, []int{2, 3, 4}, []int{2, 3, 4})

	// 丢失 1->2：B 消费的阻塞结果不可用且生产者已完成，A 被拉入。
	if err := p.LoseResult(1, 2); err != nil {
		t.Fatalf("LoseResult: %v", err)
	}
	avail, _ = p.ResultAvailable(1, 2)
	if avail {
		t.Fatalf("ResultAvailable after lose = true, want false")
	}
	plan, err = p.Fail(2)
	if err != nil {
		t.Fatalf("Fail(2): %v", err)
	}
	assertPlan(t, plan, []int{1, 2, 3, 4}, []int{1, 2, 3, 4})

	// 重启生效：1 回到运行中，丢失标记清除但结果尚未重新产出。
	if done, _ := p.IsCompleted(1); done {
		t.Fatalf("task 1 should be running again after restart")
	}
	if err := p.LoseResult(1, 2); !errors.Is(err, ErrResultNotProduced) {
		t.Fatalf("LoseResult after restart err = %v, want ErrResultNotProduced", err)
	}
	if err := p.Complete(1); err != nil {
		t.Fatalf("re-Complete(1): %v", err)
	}
	if avail, _ := p.ResultAvailable(1, 2); !avail {
		t.Fatalf("result should be available again after re-completion, lost flag must be cleared")
	}
}

func TestUpstreamStillRunningNotPulled(t *testing.T) {
	p := chainPlanner(t, nil)

	// 1 未完成（仍在运行），其结果尚未产出：规则三不拉入运行中的生产者，
	// 只有失败区域与规则二要求的下游消费区域。
	plan, err := p.Fail(2)
	if err != nil {
		t.Fatalf("Fail(2): %v", err)
	}
	assertPlan(t, plan, []int{2, 3, 4}, []int{2, 3, 4})
}

func TestMultiLevelCascadeConvergence(t *testing.T) {
	p := chainPlanner(t, nil)

	// A、B 已完成，C 保持运行以便对其上报失败；1->2、2->3 结果均丢失。fail C：
	// 第 1 轮：规则二加 D，规则三加 B；
	// 第 2 轮：B 消费的 1->2 也丢失，加 A；
	// 第 3 轮：收敛。结果 {A,B,C,D}，无关区域 E 不参与。
	for _, id := range []int{1, 2} {
		if err := p.Complete(id); err != nil {
			t.Fatalf("Complete(%d): %v", id, err)
		}
	}
	// 无关区域 E 中的 5 先完成，重启后必须保持已完成。
	if err := p.Complete(5); err != nil {
		t.Fatalf("Complete(5): %v", err)
	}
	if err := p.LoseResult(2, 3); err != nil {
		t.Fatalf("LoseResult(2,3): %v", err)
	}
	if err := p.LoseResult(1, 2); err != nil {
		t.Fatalf("LoseResult(1,2): %v", err)
	}
	plan, err := p.Fail(3)
	if err != nil {
		t.Fatalf("Fail(3): %v", err)
	}
	assertPlan(t, plan, []int{1, 2, 3, 4}, []int{1, 2, 3, 4})

	// 重启后 A-D 全部运行中；E 中已完成的 5 不受影响（不重启无关区域）。
	for _, id := range []int{1, 2, 3, 4} {
		if done, _ := p.IsCompleted(id); done {
			t.Fatalf("task %d should be running after restart", id)
		}
	}
	if done, _ := p.IsCompleted(5); !done {
		t.Fatalf("task 5 in unrelated region must remain completed/untouched")
	}
}

func TestLoseResultValidationAndIdempotency(t *testing.T) {
	p := chainPlanner(t, nil)

	if err := p.LoseResult(1, 99); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("unknown endpoint err = %v, want ErrTaskNotFound", err)
	}
	// 5->6 是流水线边：端点都存在，报非阻塞边。
	if err := p.LoseResult(5, 6); !errors.Is(err, ErrNonBlockingEdge) {
		t.Fatalf("pipeline edge err = %v, want ErrNonBlockingEdge", err)
	}
	// 4->1 有序对不存在（仅有 1->2、2->3、3->4）。
	if err := p.LoseResult(4, 1); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatalf("missing edge err = %v, want ErrEdgeNotFound", err)
	}
	// 生产者 1 仍在运行，结果尚未产出。
	if err := p.LoseResult(1, 2); !errors.Is(err, ErrResultNotProduced) {
		t.Fatalf("not produced err = %v, want ErrResultNotProduced", err)
	}

	if err := p.Complete(1); err != nil {
		t.Fatalf("Complete(1): %v", err)
	}
	if err := p.LoseResult(1, 2); err != nil {
		t.Fatalf("first lose: %v", err)
	}

	// 被拒绝的上报不得改变状态：先记录，再用一系列非法调用确认结果仍丢失。
	if err := p.LoseResult(1, 99); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("rejected lose err = %v", err)
	}
	// 重复丢失上报：成功且无变化；随后结果仍不可用。
	if err := p.LoseResult(1, 2); err != nil {
		t.Fatalf("idempotent lose: %v", err)
	}
	if avail, _ := p.ResultAvailable(1, 2); avail {
		t.Fatalf("result must stay unavailable after idempotent lose")
	}

	// 查询类接口的错误区分。
	if _, err := p.ResultAvailable(5, 6); !errors.Is(err, ErrNonBlockingEdge) {
		t.Fatalf("ResultAvailable pipeline edge err = %v", err)
	}
	if _, err := p.ResultAvailable(4, 1); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatalf("ResultAvailable missing edge err = %v", err)
	}
	if _, err := p.IsCompleted(42); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("IsCompleted err = %v", err)
	}
	if _, err := p.RestartPlanFor(42); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("RestartPlanFor err = %v", err)
	}
}

// replay 对一个新规划器重放固定上报序列。
func replay(t *testing.T) RestartPlan {
	t.Helper()
	p := chainPlanner(t, nil)
	for _, id := range []int{1, 2} {
		if err := p.Complete(id); err != nil {
			t.Fatalf("Complete(%d): %v", id, err)
		}
	}
	if err := p.LoseResult(2, 3); err != nil {
		t.Fatalf("LoseResult(2,3): %v", err)
	}
	if err := p.LoseResult(1, 2); err != nil {
		t.Fatalf("LoseResult(1,2): %v", err)
	}
	plan, err := p.Fail(3)
	if err != nil {
		t.Fatalf("Fail(3): %v", err)
	}
	return plan
}

func TestDeterminismSameSequenceSameResult(t *testing.T) {
	first := replay(t)
	for i := 0; i < 5; i++ {
		got := replay(t)
		if fmt.Sprint(got) != fmt.Sprint(first) {
			t.Fatalf("run %d: %v != %v", i, got, first)
		}
	}
	assertPlan(t, first, []int{1, 2, 3, 4}, []int{1, 2, 3, 4})
}

func TestConcurrentReportsAndQueries(t *testing.T) {
	p := chainPlanner(t, nil)

	var wg sync.WaitGroup
	// 多个 goroutine 并发混合上报与查询；-race 下验证无数据竞争，
	// 且所有错误均为已定义错误，状态始终自洽。
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				task := 1 + (g+k)%4
				if err := p.Complete(task); err != nil && !errors.Is(err, ErrTaskNotRunning) {
					t.Errorf("unexpected Complete error: %v", err)
					return
				}
				if _, err := p.Fail(task); err != nil &&
					!errors.Is(err, ErrTaskNotRunning) && !errors.Is(err, ErrTaskNotFound) {
					t.Errorf("unexpected Fail error: %v", err)
					return
				}
				if _, err := p.RestartPlanFor(task); err != nil {
					t.Errorf("unexpected RestartPlanFor error: %v", err)
					return
				}
				if _, err := p.ResultAvailable(task, task+1); err != nil &&
					!errors.Is(err, ErrEdgeNotFound) && !errors.Is(err, ErrNonBlockingEdge) {
					t.Errorf("unexpected ResultAvailable error: %v", err)
					return
				}
				_ = p.Regions()
			}
		}(g)
	}
	wg.Wait()

	// 并发结束后，规划器仍可正常工作。
	plan, err := p.RestartPlanFor(1)
	if err != nil {
		t.Fatalf("post-concurrency plan: %v", err)
	}
	assertPlan(t, plan, []int{1, 2, 3, 4}, []int{1, 2, 3, 4})
}

func TestLoggingPrintsInputOutputAndBasis(t *testing.T) {
	var buf bytes.Buffer
	p := chainPlanner(t, &buf)

	if err := p.Complete(1); err != nil {
		t.Fatalf("Complete(1): %v", err)
	}
	if err := p.LoseResult(1, 2); err != nil {
		t.Fatalf("LoseResult: %v", err)
	}
	if _, err := p.Fail(2); err != nil {
		t.Fatalf("Fail(2): %v", err)
	}

	log := buf.String()
	for _, want := range []string{
		"build graph: input",      // 输入
		"region partition",        // 区域划分判定依据
		"lose_result: input",      // 上报输入
		"now marked lost",         // 丢失判定依据
		"add producer region",     // 规则三判定依据
		"restart closure: result", // 输出
		`regions="[1 2 3 4]"`,     // 输出区域编号
	} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Errorf("log missing %q\nfull log:\n%s", want, log)
		}
	}
}
