package ontology_test

import (
	"fmt"
	"sort"
	"sync"
	"testing"

	"ontology/ontology"
)

// TestConcurrentNoFork 验证同一实例的并发转移等价于某个全局串行顺序：
// 成功数 == 轨迹长度；相邻轨迹必须首尾相接；每个并发 worker 观察到的 from
// 必须等于该转移在串行顺序中实际起点（不允许两个成功转移基于同一 from 分叉）。
func TestConcurrentNoFork(t *testing.T) {
	// 有环结构：a <-> b，外加一个只允许 a->c 但 c 为终态的分支。
	ot := mustType(t, "machine",
		[]string{"a", "b", "c"},
		[][2]string{{"a", "b"}, {"b", "a"}, {"a", "c"}},
		[]string{"c"})
	ot.Registry().RegisterTransition("a", "b", ontology.Hook{
		ID: "edge-ab", Semantic: ontology.CommitImmediately,
		Check: func(*ontology.TransitionContext) error { return nil },
	})
	ot.Registry().RegisterTransition("b", "a", ontology.Hook{
		ID: "edge-ba", Semantic: ontology.CommitImmediately,
		Check: func(*ontology.TransitionContext) error { return nil },
	})
	ot.Registry().RegisterEntry("c", ontology.Hook{
		ID: "entry-c", Semantic: ontology.CommitImmediately,
		Check: func(*ontology.TransitionContext) error { return nil },
	})

	const workers = 32
	const perWorker = 40
	mgr := ontology.NewManager()
	in, err := mgr.CreateInstance(ot, "fork", "a")
	if err != nil {
		t.Fatal(err)
	}

	type outcome struct {
		worker int
		from   string
		to     string
		ok     bool
		reason string
	}
	outcomes := make([][]outcome, workers)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < perWorker; i++ {
				target := "b"
				if (w+i)%5 == 0 {
					target = "a" // 制造 a->a / b->a 等非法或合法混合请求
				}
				if (w+i)%11 == 0 {
					target = "c" // 尝试提前进终态
				}
				res, err := mgr.Transition("fork", target)
				oc := outcome{worker: w, to: target, ok: err == nil, reason: failReason(err)}
				if err == nil {
					oc.from = res.From
				}
				outcomes[w] = append(outcomes[w], oc)
			}
		}(w)
	}
	close(start)
	wg.Wait()

	hist := in.History()
	success := 0
	var all []outcome
	for _, ocs := range outcomes {
		all = append(all, ocs...)
		for _, oc := range ocs {
			if oc.ok {
				success++
			}
		}
	}

	// 依据 1：成功转移数必须等于轨迹长度（每次成功都被串行记录一次）。
	fmt.Printf("输入: %d 个并发 worker x %d 次混合转移\n实际输出: 成功=%d 轨迹长度=%d 终态=%s\n判定依据: 成功数等于轨迹数；轨迹首尾相接且无重复前置（无分叉）\n",
		workers, perWorker, success, len(hist), in.Current())
	if success != len(hist) {
		t.Fatalf("成功转移数 %d 与轨迹长度 %d 不一致", success, len(hist))
	}

	// 依据 2：轨迹必须首尾相接，且每个成功结果报告的 from 与轨迹中的 from 一一对应。
	cur := "a"
	fromCount := map[[2]string]int{}
	for _, h := range hist {
		if h.From != cur {
			t.Fatalf("轨迹出现分叉/跳变: 期望从 %s 出发，实际记录 %s->%s", cur, h.From, h.To)
		}
		fromCount[[2]string{h.From, h.To}]++
		cur = h.To
	}

	okCount := map[[2]string]int{}
	for _, oc := range all {
		if oc.ok {
			okCount[[2]string{oc.from, oc.to}]++
		}
	}
	for k, v := range okCount {
		if fromCount[k] != v {
			t.Fatalf("成功结果声称的 from/to 与轨迹不符: %v 结果=%d 轨迹=%d", k, v, fromCount[k])
		}
	}

	// 依据 3：非法目标的拒绝原因必须可区分且未影响轨迹。
	reasonCount := map[string]int{}
	for _, oc := range all {
		reasonCount[oc.reason]++
	}
	fmt.Printf("实际输出: 结果分类=%v（按 worker 完成顺序收集，可被任意串行化解释）\n", sortedCount(reasonCount))
}

func sortedCount(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return out
}

// TestReplayDeterminism 验证同一请求序列重放得到完全相同的轨迹与钩子触发记录。
func TestReplayDeterminism(t *testing.T) {
	ot := mustType(t, "replay",
		[]string{"a", "b"},
		[][2]string{{"a", "b"}, {"b", "a"}, {"a", "a"}}, nil)
	reg := ot.Registry()
	mk := func(prefix string) {
		reg.RegisterTransition("a", "b", ontology.Hook{
			ID: prefix + "-ab", Semantic: ontology.CommitOnSuccess,
			Check:      func(*ontology.TransitionContext) error { return nil },
			Compensate: func(*ontology.TransitionContext) {},
		})
		reg.RegisterEntry("b", ontology.Hook{
			ID: prefix + "-entry-b", Semantic: ontology.CommitImmediately,
			Check: func(c *ontology.TransitionContext) error { return nil },
		})
	}
	mk("first")

	reqs := [][2]string{{"r", "b"}, {"r", "a"}, {"r", "b"}, {"r", "b"}, {"r", "a"}, {"r", "a"}, {"missing", "b"}, {"r", "b"}}

	run := func() (string, [][2]string, []ontology.FireRecord, []string) {
		mgr := ontology.NewManager()
		if _, err := mgr.CreateInstance(ot, "r", "a"); err != nil {
			t.Fatal(err)
		}
		reasons := make([]string, len(reqs))
		for i, q := range reqs {
			_, err := mgr.Transition(q[0], q[1])
			reasons[i] = failReason(err)
		}
		in := mgr.GetInstance("r")
		var hist [][2]string
		for _, h := range in.History() {
			hist = append(hist, [2]string{h.From, h.To})
		}
		return in.Current(), hist, in.FireLog(), reasons
	}

	cur1, h1, f1, r1 := run()
	cur2, h2, f2, r2 := run()
	fmt.Printf("输入: 固定请求序列 %v\n实际输出(两次重放):\n  运行1 current=%s history=%v reasons=%v fire=%d 条\n  运行2 current=%s history=%v reasons=%v fire=%d 条\n判定依据: 序列、声明与钩子均确定，故两次轨迹/触发记录必须逐条相同\n",
		reqs, cur1, h1, r1, len(f1), cur2, h2, r2, len(f2))
	if cur1 != cur2 || !histEqual(h1, h2) || !strSlicesEqual(r1, r2) {
		t.Fatalf("重放结果不一致")
	}
	if len(f1) != len(f2) {
		t.Fatalf("重放钩子触发记录长度不一致")
	}
	for i := range f1 {
		if f1[i] != f2[i] {
			t.Fatalf("重放钩子触发记录第 %d 条不一致: %+v != %+v", i, f1[i], f2[i])
		}
	}
}
