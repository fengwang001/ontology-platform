package agg

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
)

func addRow(group, value string, amount int64) Row {
	return Row{Op: OpAdd, Group: group, Value: value, Amount: amount}
}

func withdrawRow(group, value string, amount int64) Row {
	return Row{Op: OpWithdraw, Group: group, Value: value, Amount: amount}
}

func indexResults(results []Result) map[string]Result {
	m := make(map[string]Result, len(results))
	for _, r := range results {
		m[r.Group] = r
	}
	return m
}

// referenceRecompute 把全部原始行视为一个批次重算，作为基准实现。
func referenceRecompute(t *testing.T, rows []Row, maxGroups int) []Result {
	t.Helper()
	partial, err := LocalAggregate(rows)
	if err != nil {
		t.Fatalf("基准重算本地聚合失败: %v", err)
	}
	g := New(maxGroups)
	if err := g.Submit(partial); err != nil {
		t.Fatalf("基准重算全局合并失败: %v", err)
	}
	return g.Results()
}

func assertResultsEqual(t *testing.T, got, want []Result, tag string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("[%s] 结果组数不同: got %v want %v", tag, got, want)
	}
	gm := indexResults(got)
	wm := indexResults(want)
	for name, w := range wm {
		got, ok := gm[name]
		if !ok {
			t.Fatalf("[%s] 缺少组 %q", tag, name)
		}
		if got.Sum != w.Sum || got.Count != w.Count || got.DistinctCount != w.DistinctCount {
			t.Fatalf("[%s] 组 %q 指标不一致: got %+v want %+v", tag, name, got, w)
		}
		if got.Avg != w.Avg {
			t.Fatalf("[%s] 组 %q 平均值不一致: got %.20f want %.20f", tag, name, got.Avg, w.Avg)
		}
	}
}

func TestLocalRejection(t *testing.T) {
	cases := []struct {
		name string
		rows []Row
		want error
	}{
		{"invalid operation", []Row{{Op: "delete", Group: "g"}}, ErrInvalidOperation},
		{"empty group", []Row{addRow("", "a", 1)}, ErrEmptyGroup},
		{"blank group", []Row{addRow("   ", "a", 1)}, ErrEmptyGroup},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := LocalAggregate(tc.rows)
			if !errors.Is(err, tc.want) {
				t.Fatalf("拒绝原因错误: got %v want %v", err, tc.want)
			}
			if p != nil {
				t.Fatalf("被拒批次不得产出部分聚合: %s", formatPartial(p))
			}
		})
	}
}

func TestGlobalRejectsWithdrawOfMissingRow(t *testing.T) {
	// 本地阶段允许对 withdraw 计算负净增减，存在性由全局阶段判定。
	p, err := LocalAggregate([]Row{withdrawRow("g", "a", 1)})
	if err != nil || p == nil {
		t.Fatalf("本地阶段应产出负净增减部分聚合, p=%s err=%v", formatPartial(p), err)
	}
	g := New(10)
	if err := g.Submit(p); !errors.Is(err, ErrWithdrawBeforeAdd) {
		t.Fatalf("全局阶段应拒绝撤回不存在的行: got %v", err)
	}

	// 撤回次数超过现存次数：即使该值存在也要拒绝，且状态不变。
	g2 := New(10)
	seed, _ := LocalAggregate([]Row{addRow("g", "a", 1)})
	if err := g2.Submit(seed); err != nil {
		t.Fatalf("预置失败: %v", err)
	}
	bad, _ := LocalAggregate([]Row{
		withdrawRow("g", "a", 1),
		withdrawRow("g", "a", 1),
	})
	if err := g2.Submit(bad); !errors.Is(err, ErrWithdrawBeforeAdd) {
		t.Fatalf("超量撤回应被拒绝: got %v", err)
	}
	r := indexResults(g2.Results())["g"]
	if r.Count != 1 || r.Sum != 1 || r.DistinctCount != 1 {
		t.Fatalf("拒绝后状态必须不变: %+v", r)
	}
	if g2.AcceptedSubmits() != 1 {
		t.Fatalf("拒绝提交不得计入已发送计数: got %d", g2.AcceptedSubmits())
	}
}

func TestTooManyGroupsRejectedAtomically(t *testing.T) {
	g := New(2)

	seed, _ := LocalAggregate([]Row{addRow("g1", "a", 1)})
	if err := g.Submit(seed); err != nil {
		t.Fatalf("预置失败: %v", err)
	}

	// 一次提交新增两个组将超过上限：必须整体拒绝，连第一个组也不得落地。
	over := &Partial{Groups: []PartialGroup{
		{Group: "g2", SumDelta: 1, CountDelta: 1, ValueDeltas: map[string]int64{"a": 1}},
		{Group: "g3", SumDelta: 1, CountDelta: 1, ValueDeltas: map[string]int64{"a": 1}},
	}}
	if err := g.Submit(over); !errors.Is(err, ErrTooManyGroups) {
		t.Fatalf("应因组数超限拒绝: got %v", err)
	}
	if len(g.Results()) != 1 {
		t.Fatalf("超限拒绝必须整体不留痕: %v", g.Results())
	}

	// 删除组后名额释放，可再次新建。
	del, _ := LocalAggregate([]Row{withdrawRow("g1", "a", 1)})
	if err := g.Submit(del); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	again, _ := LocalAggregate([]Row{addRow("g2", "a", 1)})
	if err := g.Submit(again); err != nil {
		t.Fatalf("名额释放后应可建组: %v", err)
	}
	results := g.Results()
	if len(results) != 1 || results[0].Group != "g2" {
		t.Fatalf("删除重建结果错误: %v", results)
	}
}

func runBatched(t *testing.T, all []Row, cuts []int, maxGroups int) []Result {
	t.Helper()
	g := New(maxGroups)
	start := 0
	for _, cut := range cuts {
		chunk := all[start:cut]
		start = cut
		partial, err := LocalAggregate(chunk)
		if err != nil {
			t.Fatalf("切点 %v 下的批次非法: %v (chunk=%v)", cuts, err, chunk)
		}
		if err := g.Submit(partial); err != nil {
			t.Fatalf("切点 %v 下提交失败: %v", cuts, err)
		}
	}
	if start != len(all) {
		partial, err := LocalAggregate(all[start:])
		if err != nil {
			t.Fatalf("尾批次非法: %v", err)
		}
		if err := g.Submit(partial); err != nil {
			t.Fatalf("尾批次提交失败: %v", err)
		}
	}
	return g.Results()
}

func TestArbitraryBatchingReproducible(t *testing.T) {
	all := []Row{
		addRow("alpha", "x", 10),
		addRow("beta", "y", 2),
		addRow("alpha", "y", 4),
		addRow("alpha", "x", 6),
		withdrawRow("alpha", "x", 10),
		addRow("beta", "y", 8),
		withdrawRow("beta", "y", 2),
		addRow("alpha", "z", 1),
	}
	reference := referenceRecompute(t, all, 10)

	chunkings := [][]int{
		{},                    // 单批
		{1, 2, 3, 4, 5, 6, 7}, // 每行一批
		{4},                   // 两批
		{2, 5, 7},             // 不等长三批
		{3, 6},                // 另一种两批
	}
	for _, cuts := range chunkings {
		got := runBatched(t, all, cuts, 10)
		assertResultsEqual(t, got, reference, "fixed-chunking")
	}

	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 100; trial++ {
		// 任意位置都可作为切点：全局前缀始终合法，切批结果必须与整批重算一致。
		cuts := []int{}
		for pos := 1; pos < len(all); pos++ {
			if rng.Intn(2) == 0 {
				cuts = append(cuts, pos)
			}
		}
		got := runBatched(t, all, cuts, 10)
		assertResultsEqual(t, got, reference, "random-legal-chunking")
	}
}

func TestConcurrentSubmitAndQuery(t *testing.T) {
	const workers = 32
	const perWorker = 50
	g := New(workers * 2)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			group := "g" + itoa(id)
			for i := 0; i < perWorker; i++ {
				p, err := LocalAggregate([]Row{addRow(group, "v", int64(id+i))})
				if err != nil {
					t.Errorf("本地聚合失败: %v", err)
					return
				}
				if err := g.Submit(p); err != nil {
					t.Errorf("并发提交失败: %v", err)
					return
				}
			}
		}(w)
	}

	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				results := g.Results()
				// 并发期间每个存活组都必须保持内部一致。
				for _, r := range results {
					if r.Count <= 0 || r.DistinctCount <= 0 {
						t.Errorf("查询到不一致的中间结果: %+v", r)
						return
					}
					if r.Avg != float64(r.Sum)/float64(r.Count) {
						t.Errorf("中间结果平均值与和/计数不符: %+v", r)
						return
					}
				}
			}
		}
	}()

	wg.Wait()
	close(stop)

	results := g.Results()
	if len(results) != workers {
		t.Fatalf("并发合并后组数错误: got %d want %d", len(results), workers)
	}
	for id := 0; id < workers; id++ {
		r := indexResults(results)["g"+itoa(id)]
		if r.Count != perWorker {
			t.Fatalf("组 g%d 计数错误: got %d want %d", id, r.Count, perWorker)
		}
		if r.DistinctCount != 1 {
			t.Fatalf("组 g%d 去重计数错误: got %d want 1", id, r.DistinctCount)
		}
		var wantSum int64
		for i := 0; i < perWorker; i++ {
			wantSum += int64(id + i)
		}
		if r.Sum != wantSum {
			t.Fatalf("组 g%d 求和错误: got %d want %d", id, r.Sum, wantSum)
		}
	}
	if g.AcceptedSubmits() != workers*perWorker {
		t.Fatalf("已接受提交计数错误: got %d want %d",
			g.AcceptedSubmits(), workers*perWorker)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestFourMetricsMerge(t *testing.T) {
	g := New(10)

	p1, err := LocalAggregate([]Row{
		addRow("g1", "a", 10),
		addRow("g1", "b", 20),
		addRow("g1", "a", 30),
	})
	if err != nil {
		t.Fatalf("批1本地聚合失败: %v", err)
	}
	if err := g.Submit(p1); err != nil {
		t.Fatalf("批1提交失败: %v", err)
	}

	p2, err := LocalAggregate([]Row{
		addRow("g1", "b", 5),
		withdrawRow("g1", "a", 10),
	})
	if err != nil {
		t.Fatalf("批2本地聚合失败: %v", err)
	}
	if err := g.Submit(p2); err != nil {
		t.Fatalf("批2提交失败: %v", err)
	}

	r, ok := indexResults(g.Results())["g1"]
	if !ok {
		t.Fatal("结果中缺少组 g1")
	}
	if r.Sum != 55 {
		t.Fatalf("求和合并错误: got %d want 55 (10+20+30+5-10)", r.Sum)
	}
	if r.Count != 3 {
		t.Fatalf("计数合并错误: got %d want 3", r.Count)
	}
	wantAvg := float64(55) / float64(3)
	if r.Avg != wantAvg {
		t.Fatalf("平均值应在和与计数合并后相除: got %v want %v", r.Avg, wantAvg)
	}
	if r.DistinctCount != 2 {
		t.Fatalf("去重计数错误: got %d want 2 (a,b 均有现存行)", r.DistinctCount)
	}
}

func TestDistinctCountTrackedPerValue(t *testing.T) {
	g := New(10)

	p, _ := LocalAggregate([]Row{
		addRow("g", "a", 1),
		addRow("g", "a", 1),
		addRow("g", "b", 1),
	})
	if err := g.Submit(p); err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	r := indexResults(g.Results())["g"]
	if r.DistinctCount != 2 {
		t.Fatalf("去重计数错误: got %d want 2", r.DistinctCount)
	}

	p, _ = LocalAggregate([]Row{withdrawRow("g", "a", 1)})
	if err := g.Submit(p); err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	r = indexResults(g.Results())["g"]
	if r.DistinctCount != 2 {
		t.Fatalf("部分撤回后去重计数错误: got %d want 2", r.DistinctCount)
	}

	p, _ = LocalAggregate([]Row{withdrawRow("g", "a", 1)})
	if err := g.Submit(p); err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	r = indexResults(g.Results())["g"]
	if r.DistinctCount != 1 {
		t.Fatalf("全部撤回后去重计数错误: got %d want 1", r.DistinctCount)
	}
	if r.Count != 1 || r.Sum != 1 {
		t.Fatalf("撤回后和/计数错误: sum=%d count=%d", r.Sum, r.Count)
	}
}

func TestAllZeroGroupNotSent(t *testing.T) {
	p, err := LocalAggregate([]Row{
		addRow("g", "a", 7),
		withdrawRow("g", "a", 7),
	})
	if err != nil {
		t.Fatalf("批内对冲不应报错: %v", err)
	}
	if len(p.Groups) != 0 {
		t.Fatalf("全零组不得发送: got %d 个部分组 %s", len(p.Groups), formatPartial(p))
	}

	p, err = LocalAggregate([]Row{
		addRow("g", "a", 1),
		withdrawRow("g", "a", 1),
		addRow("g", "b", 2),
	})
	if err != nil {
		t.Fatalf("本地聚合失败: %v", err)
	}
	if len(p.Groups) != 1 {
		t.Fatalf("应只发送一个非全零组: got %d", len(p.Groups))
	}
	if _, present := p.Groups[0].ValueDeltas["a"]; present {
		t.Fatalf("净增减为 0 的值条目不得发送: %s", formatDeltas(p.Groups[0].ValueDeltas))
	}
}

func TestGroupDeleteAndRebuild(t *testing.T) {
	g := New(10)

	p, _ := LocalAggregate([]Row{
		addRow("g", "a", 3),
		addRow("g", "b", 4),
	})
	if err := g.Submit(p); err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if len(g.Results()) != 1 {
		t.Fatal("建组失败")
	}

	p, _ = LocalAggregate([]Row{withdrawRow("g", "a", 3)})
	if err := g.Submit(p); err != nil {
		t.Fatalf("撤回 a 失败: %v", err)
	}
	p, _ = LocalAggregate([]Row{withdrawRow("g", "b", 4)})
	if err := g.Submit(p); err != nil {
		t.Fatalf("撤回 b 失败: %v", err)
	}
	if len(g.Results()) != 0 {
		t.Fatalf("计数归零的组必须从结果删除，got %v", g.Results())
	}

	p, _ = LocalAggregate([]Row{addRow("g", "c", 9)})
	if err := g.Submit(p); err != nil {
		t.Fatalf("重建组失败: %v", err)
	}
	r := indexResults(g.Results())["g"]
	if r.Sum != 9 || r.Count != 1 || r.DistinctCount != 1 {
		t.Fatalf("重建后状态错误: %+v", r)
	}
	if r.Avg != 9 {
		t.Fatalf("重建后平均值错误: got %v want 9", r.Avg)
	}
}

func TestInvalidInputsRejectedAndStateUntouched(t *testing.T) {
	distinct := map[error]bool{
		ErrInvalidOperation:  true,
		ErrEmptyGroup:        true,
		ErrWithdrawBeforeAdd: true,
		ErrTooManyGroups:     true,
	}
	if len(distinct) != 4 {
		t.Fatal("四类错误必须互不相同")
	}

	cases := []struct {
		name    string
		partial *Partial
		want    error
	}{
		{
			name: "empty group name",
			partial: &Partial{Groups: []PartialGroup{{
				Group:       "  ",
				SumDelta:    1,
				CountDelta:  1,
				ValueDeltas: map[string]int64{"a": 1},
			}}},
			want: ErrEmptyGroup,
		},
		{
			name: "zero delta payload",
			partial: &Partial{Groups: []PartialGroup{{
				Group:       "g",
				ValueDeltas: map[string]int64{"a": 0},
			}}},
			want: ErrInvalidOperation,
		},
		{
			name: "withdraw missing value",
			partial: &Partial{Groups: []PartialGroup{{
				Group:       "g",
				SumDelta:    -1,
				CountDelta:  -1,
				ValueDeltas: map[string]int64{"a": -1},
			}}},
			want: ErrWithdrawBeforeAdd,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := New(10)
			seed, _ := LocalAggregate([]Row{addRow("seed", "x", 5)})
			if err := g.Submit(seed); err != nil {
				t.Fatalf("预置状态失败: %v", err)
			}

			err := g.Submit(tc.partial)
			if !errors.Is(err, tc.want) {
				t.Fatalf("拒绝原因错误: got %v want %v", err, tc.want)
			}

			results := g.Results()
			if len(results) != 1 || results[0].Group != "seed" || results[0].Sum != 5 {
				t.Fatalf("拒绝后全局状态发生变化: %v", results)
			}
			if g.AcceptedSubmits() != 1 || g.SentPartialGroups() != 1 {
				t.Fatalf("拒绝后已发送计数发生变化: accepted=%d sentGroups=%d",
					g.AcceptedSubmits(), g.SentPartialGroups())
			}
		})
	}
}
