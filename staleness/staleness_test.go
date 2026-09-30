package staleness

import (
	"errors"
	"sync"
	"testing"
)

func mustAsset(t *testing.T, b *Builder, name string, first, last int) {
	t.Helper()
	if err := b.AddAsset(name, first, last); err != nil {
		t.Fatalf("AddAsset(%s) 失败: %v", name, err)
	}
}

func mustEdge(t *testing.T, b *Builder, up, down string, lo, hi int) {
	t.Helper()
	if err := b.AddEdge(up, down, lo, hi); err != nil {
		t.Fatalf("AddEdge(%s->%s) 失败: %v", up, down, err)
	}
}

func mustWrite(t *testing.T, e *Engine, asset string, part int) {
	t.Helper()
	if err := e.ExternalWrite(asset, part); err != nil {
		t.Fatalf("ExternalWrite(%s,%d) 失败: %v", asset, part, err)
	}
}

func mustRun(t *testing.T, e *Engine, asset string, part int) {
	t.Helper()
	id, err := e.StartRun(asset, part)
	if err != nil {
		t.Fatalf("StartRun(%s,%d) 失败: %v", asset, part, err)
	}
	if err := e.CompleteRun(id, true); err != nil {
		t.Fatalf("CompleteRun(%d) 失败: %v", id, err)
	}
}

func refsEqual(a, b []PartitionRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func logInspect(t *testing.T, e *Engine, asset string, part int, why string) {
	t.Helper()
	m, v, s, err := e.Inspect(asset, part)
	if err != nil {
		t.Fatalf("Inspect(%s,%d) 失败: %v", asset, part, err)
	}
	t.Logf("判定 %s@%d: materialized=%v version=%d stale=%v （依据：%s）",
		asset, part, m, v, s, why)
}

// 运行期间输入被改写，结果一落地即过期。
func TestStaleOnLandingWhenInputRewrittenDuringRun(t *testing.T) {
	b := NewBuilder()
	mustAsset(t, b, "U", 0, 10)
	mustAsset(t, b, "D", 0, 10)
	mustEdge(t, b, "U", "D", 0, 0)
	e, err := b.Build()
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}

	mustWrite(t, e, "U", 5) // U@5 版本 1
	id, err := e.StartRun("D", 5)
	if err != nil {
		t.Fatalf("StartRun 失败: %v", err)
	}
	t.Logf("输入：D@5 开始时记录消费版本 U@5=1")
	mustWrite(t, e, "U", 5) // 运行期间 U@5 被改写为版本 2
	t.Logf("输入：运行期间外部写入 U@5，版本升为 2")
	if err := e.CompleteRun(id, true); err != nil {
		t.Fatalf("CompleteRun 失败: %v", err)
	}

	stale, err := e.IsStale("D", 5)
	if err != nil {
		t.Fatalf("IsStale 失败: %v", err)
	}
	t.Logf("输出：D@5 stale=%v", stale)
	if !stale {
		t.Fatal("D@5 应当在落地瞬间即过期：消费记录 U@5=1 与当前版本 2 不等")
	}
	logInspect(t, e, "D", 5, "消费版本 U@5=1 != 当前 U@5=2")
}

// 失败完成不改变任何状态。
func TestFailedCompletionKeepsState(t *testing.T) {
	b := NewBuilder()
	mustAsset(t, b, "U", 0, 3)
	mustAsset(t, b, "D", 0, 3)
	mustEdge(t, b, "U", "D", 0, 0)
	e, err := b.Build()
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}

	mustWrite(t, e, "U", 0)
	mustRun(t, e, "D", 0) // D@0 版本 1
	id, err := e.StartRun("D", 0)
	if err != nil {
		t.Fatalf("StartRun 失败: %v", err)
	}
	if err := e.CompleteRun(id, false); err != nil {
		t.Fatalf("CompleteRun(false) 失败: %v", err)
	}
	m, v, s, err := e.Inspect("D", 0)
	if err != nil {
		t.Fatalf("Inspect 失败: %v", err)
	}
	t.Logf("输出：失败后 D@0 materialized=%v version=%d stale=%v", m, v, s)
	if !m || v != 1 || s {
		t.Fatalf("失败完成不应改变状态，得到 m=%v v=%d s=%v", m, v, s)
	}
	if _, err := e.StartRun("D", 0); err != nil {
		t.Fatalf("失败完成后应能重新开始: %v", err)
	}
}

// 偏移为负且跨度大于一时，影响面沿依赖边正向传播到正确的下游分区。
func TestImpactNegativeOffsetAndWideSpan(t *testing.T) {
	b := NewBuilder()
	mustAsset(t, b, "U", 0, 10)
	mustAsset(t, b, "D", 0, 10)
	// D@d 读取 U@[d-2, d+1]，跨度 4，含负偏移。
	mustEdge(t, b, "U", "D", -2, 1)
	e, err := b.Build()
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}

	for p := 0; p <= 10; p++ {
		mustWrite(t, e, "U", p)
	}
	for _, d := range []int{3, 4, 5, 6, 7, 8} {
		mustRun(t, e, "D", d)
	}
	t.Logf("输入：U@0..10 已物化，D@3..8 已物化且新鲜；查询重写 U@5 的影响面")

	impact, err := e.Impact("U", 5)
	if err != nil {
		t.Fatalf("Impact 失败: %v", err)
	}
	t.Logf("输出：Impact(U@5)=%v", impact)
	// U@5 被读取当且仅当 d-2<=5<=d+1，即 d 属于 [4,7]。
	want := []PartitionRef{{"D", 4}, {"D", 5}, {"D", 6}, {"D", 7}}
	if !refsEqual(impact, want) {
		t.Fatalf("影响面方向错误：得到 %v，期望 %v", impact, want)
	}
	t.Logf("依据：D@d 消费 U@[d-2,d+1]，5 落在 d=4..7 的窗口内；D@3 读 U@1..4、D@8 读 U@6..9 均不受影响")
}

// 输入过期导致下游不可开始，且错误列出全部缺失或过期输入。
func TestStaleInputBlocksStart(t *testing.T) {
	b := NewBuilder()
	mustAsset(t, b, "U", 0, 5)
	mustAsset(t, b, "D", 0, 5)
	mustAsset(t, b, "E", 0, 5)
	mustEdge(t, b, "U", "D", 0, 0)
	mustEdge(t, b, "D", "E", 0, 0)
	e, err := b.Build()
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}

	mustWrite(t, e, "U", 2)
	mustRun(t, e, "D", 2)
	mustRun(t, e, "E", 2)
	mustWrite(t, e, "U", 2) // U@2 版本 2，D@2、E@2 传递过期
	t.Logf("输入：U@2 被改写，D@2 直接过期，E@2 经 D@2 传递过期")

	_, err = e.StartRun("E", 2)
	t.Logf("输出：StartRun(E,2) err=%v", err)
	var nerr *Error
	if !errors.As(err, &nerr) || nerr.Kind != KindInputsNotReady {
		t.Fatalf("期望 KindInputsNotReady，得到 %v", err)
	}
	if !refsEqual(nerr.Stale, []PartitionRef{{"D", 2}}) || len(nerr.Missing) != 0 {
		t.Fatalf("未就绪输入列表错误：stale=%v missing=%v", nerr.Stale, nerr.Missing)
	}
	t.Logf("依据：E@2 的输入 D@2 已过期，列入 Stale；缺失列表为空")

	// 缺失输入同样阻塞并列出。
	_, err = e.StartRun("D", 3)
	t.Logf("输出：StartRun(D,3) err=%v", err)
	if !errors.As(err, &nerr) || nerr.Kind != KindInputsNotReady ||
		!refsEqual(nerr.Missing, []PartitionRef{{"U", 3}}) {
		t.Fatalf("期望缺失输入 U@3，得到 %v (missing=%v)", err, nerr.Missing)
	}

	// 被拒绝的开始不改变状态：修复输入后仍可开始。
	mustRun(t, e, "D", 2) // 重跑 D@2 使其新鲜
	id, err := e.StartRun("E", 2)
	if err != nil {
		t.Fatalf("D 修复后 E 应可开始: %v", err)
	}
	if err := e.CompleteRun(id, true); err != nil {
		t.Fatalf("CompleteRun 失败: %v", err)
	}
	logInspect(t, e, "E", 2, "D@2 重跑后 E@2 重新物化，消费记录与当前版本一致")
}

// 规划不含新鲜分区，只含缺失或过期者，且按（层深，资产名，分区号）升序。
func TestPlanBackfillExcludesFreshAndOrdersByDepth(t *testing.T) {
	b := NewBuilder()
	mustAsset(t, b, "A", 0, 3)
	mustAsset(t, b, "B", 0, 3)
	mustAsset(t, b, "C", 0, 3)
	mustAsset(t, b, "D", 0, 3)
	mustEdge(t, b, "A", "C", 0, 0)
	mustEdge(t, b, "B", "D", 0, 0)
	mustEdge(t, b, "C", "D", 0, 0)
	e, err := b.Build()
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}
	// 层深：A,B=0；C=1；D=2。

	for _, p := range []int{0, 1} {
		mustWrite(t, e, "A", p)
		mustWrite(t, e, "B", p)
		mustRun(t, e, "C", p)
		mustRun(t, e, "D", p)
	}

	plan, err := e.PlanBackfill([]PartitionRef{{"D", 0}, {"D", 1}})
	if err != nil {
		t.Fatalf("PlanBackfill 失败: %v", err)
	}
	t.Logf("输出：全新鲜时 PlanBackfill(D@0,D@1)=%v", plan)
	if len(plan) != 0 {
		t.Fatalf("新鲜分区一律不进入规划，得到 %v", plan)
	}

	mustWrite(t, e, "A", 1) // C@1、D@1 传递过期
	plan, err = e.PlanBackfill([]PartitionRef{{"D", 0}, {"D", 1}})
	if err != nil {
		t.Fatalf("PlanBackfill 失败: %v", err)
	}
	t.Logf("输出：A@1 改写后 PlanBackfill(D@0,D@1)=%v", plan)
	want := []PartitionRef{{"C", 1}, {"D", 1}}
	if !refsEqual(plan, want) {
		t.Fatalf("规划应只含过期的 C@1、D@1，得到 %v", plan)
	}
	t.Logf("依据：D@0 链新鲜被排除；A@1 是新鲜源分区不进入；C@1、D@1 过期进入")

	// 全缺失目标：规划含整条链，按层深排序。
	plan, err = e.PlanBackfill([]PartitionRef{{"D", 2}})
	if err != nil {
		t.Fatalf("PlanBackfill 失败: %v", err)
	}
	t.Logf("输出：PlanBackfill(D@2)=%v", plan)
	want = []PartitionRef{{"A", 2}, {"B", 2}, {"C", 2}, {"D", 2}}
	if !refsEqual(plan, want) {
		t.Fatalf("层深次序错误：得到 %v，期望 %v", plan, want)
	}
	t.Logf("依据：层深 A,B=0（同名按资产名升序），C=1，D=2；全部缺失均需物化")
}

// 菱形依赖下层深为上游层深最大值加一，规划与影响面均按（层深，资产名，分区号）排序。
func TestDepthOrderDiamond(t *testing.T) {
	b := NewBuilder()
	mustAsset(t, b, "S", 0, 0)
	mustAsset(t, b, "B", 0, 0)
	mustAsset(t, b, "A", 0, 0) // 名字靠前但层深靠后
	mustAsset(t, b, "Z", 0, 0)
	mustEdge(t, b, "S", "Z", 0, 0)
	mustEdge(t, b, "S", "B", 0, 0)
	mustEdge(t, b, "Z", "A", 0, 0)
	mustEdge(t, b, "B", "A", 0, 0)
	e, err := b.Build()
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}
	// 层深：S=0，B,Z=1，A=2。

	plan, err := e.PlanBackfill([]PartitionRef{{"A", 0}})
	if err != nil {
		t.Fatalf("PlanBackfill 失败: %v", err)
	}
	t.Logf("输出：全缺失时 PlanBackfill(A@0)=%v", plan)
	want := []PartitionRef{{"S", 0}, {"B", 0}, {"Z", 0}, {"A", 0}}
	if !refsEqual(plan, want) {
		t.Fatalf("层深次序错误：得到 %v，期望 %v", plan, want)
	}

	mustWrite(t, e, "S", 0)
	mustRun(t, e, "B", 0)
	mustRun(t, e, "Z", 0)
	mustRun(t, e, "A", 0)
	impact, err := e.Impact("S", 0)
	if err != nil {
		t.Fatalf("Impact 失败: %v", err)
	}
	t.Logf("输出：Impact(S@0)=%v", impact)
	want = []PartitionRef{{"B", 0}, {"Z", 0}, {"A", 0}}
	if !refsEqual(impact, want) {
		t.Fatalf("影响面次序错误：得到 %v，期望 %v", impact, want)
	}
	t.Logf("依据：层深 B,Z=1 先于 A=2；同层按资产名 B<Z；S 自身不是下游不进入")
}

// 已过期的下游不算“将变为过期”，不进入影响面。
func TestImpactExcludesAlreadyStale(t *testing.T) {
	b := NewBuilder()
	mustAsset(t, b, "U", 0, 0)
	mustAsset(t, b, "V", 0, 0)
	mustAsset(t, b, "D", 0, 0)
	mustEdge(t, b, "U", "D", 0, 0)
	mustEdge(t, b, "V", "D", 0, 0)
	e, err := b.Build()
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}

	mustWrite(t, e, "U", 0)
	mustWrite(t, e, "V", 0)
	mustRun(t, e, "D", 0)
	mustWrite(t, e, "V", 0) // D@0 因 V 已过期
	t.Logf("输入：D@0 因 V@0 改写已过期；查询重写 U@0 的影响面")

	impact, err := e.Impact("U", 0)
	if err != nil {
		t.Fatalf("Impact 失败: %v", err)
	}
	t.Logf("输出：Impact(U@0)=%v", impact)
	if len(impact) != 0 {
		t.Fatalf("D@0 已过期，不算将变为过期，得到 %v", impact)
	}
	t.Logf("依据：影响面 = 模拟重写后的过期集合减去重写前的过期集合")
}

func checkKind(t *testing.T, err error, kind Kind, what string) {
	t.Helper()
	var nerr *Error
	if !errors.As(err, &nerr) || nerr.Kind != kind {
		t.Fatalf("%s：期望 Kind=%d，得到 %v", what, kind, err)
	}
	t.Logf("拒绝 %s：kind=%d msg=%s", what, nerr.Kind, nerr.Message)
}

// 建图期各类非法输入按可区分的原因拒绝。
func TestBuilderValidation(t *testing.T) {
	b := NewBuilder()
	checkKind(t, b.AddAsset("X", 5, 1), KindInvalidRange, "first>last")
	mustAsset(t, b, "X", 0, 1)
	checkKind(t, b.AddAsset("X", 0, 1), KindDuplicateAsset, "重复资产")
	checkKind(t, b.AddEdge("ghost", "X", 0, 0), KindUnknownAsset, "边上上游未知资产")
	checkKind(t, b.AddEdge("X", "ghost", 0, 0), KindUnknownAsset, "边下游未知资产")
	mustAsset(t, b, "Y", 0, 1)
	mustEdge(t, b, "X", "Y", 0, 0)
	checkKind(t, b.AddEdge("X", "Y", 0, 0), KindDuplicateEdge, "重复边")
	mustAsset(t, b, "Z", 0, 1)
	checkKind(t, b.AddEdge("X", "Z", 2, 1), KindInvalidOffset, "lo>hi")

	// 依赖成环：A->B->C->A。
	bc := NewBuilder()
	mustAsset(t, bc, "A", 0, 0)
	mustAsset(t, bc, "B", 0, 0)
	mustAsset(t, bc, "C", 0, 0)
	mustEdge(t, bc, "A", "B", 0, 0)
	mustEdge(t, bc, "B", "C", 0, 0)
	mustEdge(t, bc, "C", "A", 0, 0)
	_, err := bc.Build()
	checkKind(t, err, KindCycle, "依赖成环")

	// 自依赖也是环。
	bs := NewBuilder()
	mustAsset(t, bs, "S", 0, 0)
	mustEdge(t, bs, "S", "S", 0, 0)
	_, err = bs.Build()
	checkKind(t, err, KindCycle, "自依赖")
}

// 操作期各类非法输入整体拒绝，且不改变任何状态。
func TestOperationValidation(t *testing.T) {
	b := NewBuilder()
	mustAsset(t, b, "U", 0, 2)
	mustAsset(t, b, "D", 0, 2)
	mustEdge(t, b, "U", "D", 0, 0)
	e, err := b.Build()
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}

	checkKind(t, e.ExternalWrite("U", 3), KindPartitionOutOfRange, "分区越界")
	checkKind(t, e.ExternalWrite("ghost", 0), KindUnknownAsset, "写入未知资产")
	checkKind(t, e.ExternalWrite("D", 0), KindNonSourceExternalWrite, "非源外部写入")
	checkKind(t, e.CompleteRun(42, true), KindRunNotFound, "运行号不存在")
	_, err = e.IsStale("ghost", 0)
	checkKind(t, err, KindUnknownAsset, "判定未知资产")
	_, err = e.PlanBackfill([]PartitionRef{{"D", 0}, {"U", 9}})
	checkKind(t, err, KindPartitionOutOfRange, "规划目标越界整体拒绝")
	_, err = e.Impact("D", 9)
	checkKind(t, err, KindPartitionOutOfRange, "影响面分区越界")

	// 被拒绝的写入不改变状态。
	m, v, _, err := e.Inspect("D", 0)
	if err != nil || m || v != 0 {
		t.Fatalf("被拒绝的操作不应改变状态：m=%v v=%d err=%v", m, v, err)
	}

	mustWrite(t, e, "U", 0)
	id, err := e.StartRun("D", 0)
	if err != nil {
		t.Fatalf("StartRun 失败: %v", err)
	}
	_, err = e.StartRun("D", 0)
	checkKind(t, err, KindAlreadyRunning, "重复开始")
	if err := e.CompleteRun(id, true); err != nil {
		t.Fatalf("CompleteRun 失败: %v", err)
	}
	checkKind(t, e.CompleteRun(id, true), KindRunNotFound, "运行已结束")

	// 被拒绝的重复开始不改变运行状态：原运行号正常完成即版本加一。
	_, v, _, err = e.Inspect("D", 0)
	if err != nil || v != 1 {
		t.Fatalf("状态异常：v=%d err=%v", v, err)
	}
	t.Logf("依据：全部非法操作整体拒绝，Inspect 确认状态未被污染")
}

// 同一分区并发开始只成功一次；混合并发调用在 -race 下安全，
// 且每次判定基于一致快照（互斥锁串行化保证与某个串行顺序一致）。
func TestConcurrentStartAndMixedOps(t *testing.T) {
	b := NewBuilder()
	mustAsset(t, b, "U", 0, 9)
	mustAsset(t, b, "D", 0, 9)
	mustEdge(t, b, "U", "D", 0, 0)
	e, err := b.Build()
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}
	for p := 0; p <= 9; p++ {
		mustWrite(t, e, "U", p)
	}

	const workers = 32
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	kinds := map[Kind]int{}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := e.StartRun("D", 0)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successes++
				return
			}
			var nerr *Error
			if !errors.As(err, &nerr) {
				t.Errorf("非预期错误类型: %v", err)
				return
			}
			kinds[nerr.Kind]++
		}(i)
	}
	wg.Wait()
	t.Logf("输出：并发开始成功 %d 次，拒绝分布 %v", successes, kinds)
	if successes != 1 || kinds[KindAlreadyRunning] != workers-1 {
		t.Fatalf("同一分区并发开始应只成功一次：successes=%d kinds=%v", successes, kinds)
	}

	// 混合并发：写入、判定、规划、影响面同时进行。
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := i % 10
			switch i % 4 {
			case 0:
				_ = e.ExternalWrite("U", p)
			case 1:
				_, _ = e.IsStale("D", p)
			case 2:
				_, _ = e.PlanBackfill([]PartitionRef{{"D", p}})
			case 3:
				_, _ = e.Impact("U", p)
			}
		}(i)
	}
	wg.Wait()

	// 并发结束后判定必须与按定义逐分区重算一致：
	// U@0 被改写偶数次则 D@0 新鲜，奇数次则过期。
	writes := 0
	for i := 0; i < workers; i++ {
		if i%4 == 0 && i%10 == 0 {
			writes++
		}
	}
	stale, err := e.IsStale("D", 0)
	if err != nil {
		t.Fatalf("IsStale 失败: %v", err)
	}
	// D@0 尚未完成过物化（上面的并发开始未 Complete），属于缺失而非过期。
	t.Logf("输出：D@0 stale=%v（U@0 被写 %d 次）", stale, writes)
	if stale {
		t.Fatal("D@0 未物化，缺失不算过期")
	}
	m, _, _, _ := e.Inspect("D", 0)
	if m {
		t.Fatal("D@0 有运行中的物化但未完成，不应已物化")
	}
}

// 传递过期链与“未物化不算过期”。
func TestTransitiveStaleAndMissingNotStale(t *testing.T) {
	b := NewBuilder()
	for _, name := range []string{"A", "B", "C"} {
		mustAsset(t, b, name, 0, 1)
	}
	mustEdge(t, b, "A", "B", 0, 0)
	mustEdge(t, b, "B", "C", 0, 0)
	e, err := b.Build()
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}

	stale, err := e.IsStale("C", 1)
	if err != nil {
		t.Fatalf("IsStale 失败: %v", err)
	}
	t.Logf("输出：未物化的 C@1 stale=%v", stale)
	if stale {
		t.Fatal("未物化者为缺失，不算过期")
	}

	mustWrite(t, e, "A", 0)
	mustRun(t, e, "B", 0)
	mustRun(t, e, "C", 0)
	mustWrite(t, e, "A", 0)
	for _, name := range []string{"B", "C"} {
		stale, err := e.IsStale(name, 0)
		if err != nil {
			t.Fatalf("IsStale 失败: %v", err)
		}
		t.Logf("输出：%s@0 stale=%v", name, stale)
		if !stale {
			t.Fatalf("%s@0 应传递过期", name)
		}
	}
	t.Logf("依据：B@0 消费记录 A@0=1 != 当前 2；C@0 的输入 B@0 自身过期")
}
