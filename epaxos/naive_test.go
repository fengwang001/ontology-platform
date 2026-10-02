package epaxos

import (
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

// mrec 是朴素参考实现使用的实例记录。
type mrec struct {
	seq      int
	deps     []Instance
	executed bool
}

func mless(recs map[Instance]*mrec, a, b Instance) bool {
	ra, rb := recs[a], recs[b]
	if ra.seq != rb.seq {
		return ra.seq < rb.seq
	}
	if a.R != b.R {
		return a.R < b.R
	}
	return a.I < b.I
}

// naiveExecute 用可达闭包求等价类的朴素方法复算一次 Execute：
// 可执行条件、分量划分与排序规则均独立实现，用于对拍。
// 返回执行序列与被阻塞集合（判定依据）。
func naiveExecute(recs map[Instance]*mrec) (out []Instance, blocked []Instance) {
	adj := make(map[Instance][]Instance)
	missing := make(map[Instance]bool)
	var nodes []Instance
	for in, r := range recs {
		if r.executed {
			continue
		}
		nodes = append(nodes, in)
		for _, d := range r.deps {
			dr, ok := recs[d]
			switch {
			case !ok:
				missing[in] = true
			case !dr.executed:
				adj[in] = append(adj[in], d)
			}
		}
	}

	// 每个节点的可达闭包（含自身）。
	reach := make(map[Instance]map[Instance]bool)
	var dfs func(u, v Instance)
	for _, u := range nodes {
		seen := map[Instance]bool{u: true}
		dfs = func(u, v Instance) {
			for _, w := range adj[v] {
				if !seen[w] {
					seen[w] = true
					dfs(u, w)
				}
			}
		}
		dfs(u, u)
		reach[u] = seen
	}

	isBlocked := make(map[Instance]bool)
	for _, u := range nodes {
		for v := range reach[u] {
			if missing[v] {
				isBlocked[u] = true
			}
		}
	}
	var exec []Instance
	for _, u := range nodes {
		if isBlocked[u] {
			blocked = append(blocked, u)
		} else {
			exec = append(exec, u)
		}
	}
	slices.SortFunc(blocked, func(a, b Instance) int {
		if a.R != b.R {
			return a.R - b.R
		}
		return a.I - b.I
	})

	// 等价类：u~v 当且仅当互相可达。
	assigned := make(map[Instance]bool)
	var classes [][]Instance
	for _, u := range exec {
		if assigned[u] {
			continue
		}
		var cls []Instance
		for _, v := range exec {
			if reach[u][v] && reach[v][u] {
				cls = append(cls, v)
				assigned[v] = true
			}
		}
		classes = append(classes, cls)
	}
	classOf := make(map[Instance]int)
	for ci, cls := range classes {
		for _, m := range cls {
			classOf[m] = ci
		}
	}
	// 类间依赖：类 C 依赖类 D 当且仅当存在边 C->D。
	deps := make([]map[int]bool, len(classes))
	for i := range deps {
		deps[i] = make(map[int]bool)
	}
	for u, ds := range adj {
		cu, ok := classOf[u]
		if !ok {
			continue
		}
		for _, d := range ds {
			if cd, ok := classOf[d]; ok && cd != cu {
				deps[cu][cd] = true
			}
		}
	}
	head := make([]Instance, len(classes))
	for i, cls := range classes {
		h := cls[0]
		for _, m := range cls[1:] {
			if mless(recs, m, h) {
				h = m
			}
		}
		head[i] = h
	}
	done := make([]bool, len(classes))
	for remaining := len(classes); remaining > 0; remaining-- {
		best := -1
		for i := range classes {
			if done[i] || len(deps[i]) > 0 {
				continue
			}
			if best == -1 || mless(recs, head[i], head[best]) {
				best = i
			}
		}
		members := classes[best]
		slices.SortFunc(members, func(a, b Instance) int {
			if mless(recs, a, b) {
				return -1
			}
			return 1
		})
		out = append(out, members...)
		done[best] = true
		for i := range classes {
			delete(deps[i], best)
		}
	}
	return out, blocked
}

func fmtInsts(xs []Instance) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = x.String()
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// 随机依赖图上与朴素可达闭包实现对拍，日志打印输入、输出与判定依据。
func TestFuzzAgainstNaive(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 1 + rng.Intn(3)
		slotMax := 3 + rng.Intn(3)
		e, err := NewExecutor(n, 1000)
		if err != nil {
			t.Fatal(err)
		}
		model := make(map[Instance]*mrec)
		randInst := func() Instance {
			return inst(rng.Intn(n), 1+rng.Intn(slotMax))
		}
		var commits []string
		round := 0
		check := func() {
			round++
			gotReal := e.Execute()
			gotNaive, blocked := naiveExecute(model)
			for _, u := range gotNaive {
				model[u].executed = true
			}
			t.Logf("seed=%d round=%d 输出 real=%v naive=%v 判定依据: 被阻塞集合=%v",
				seed, round, fmtInsts(gotReal), fmtInsts(gotNaive), fmtInsts(blocked))
			if !slices.Equal(gotReal, gotNaive) {
				t.Fatalf("seed=%d round=%d: real=%v naive=%v (blocked=%v)",
					seed, round, gotReal, gotNaive, blocked)
			}
			var wantPending []Instance
			for in, r := range model {
				if !r.executed {
					wantPending = append(wantPending, in)
				}
			}
			slices.SortFunc(wantPending, func(a, b Instance) int {
				if a.R != b.R {
					return a.R - b.R
				}
				return a.I - b.I
			})
			if got := e.Pending(); !slices.Equal(got, wantPending) {
				t.Fatalf("seed=%d round=%d: Pending()=%v, want %v", seed, round, got, wantPending)
			}
		}
		ops := 15 + rng.Intn(15)
		for k := 0; k < ops; k++ {
			in := randInst()
			seq := 1 + rng.Intn(8)
			var deps []Instance
			seen := map[Instance]bool{in: true}
			for j := rng.Intn(4); j > 0; j-- {
				d := randInst()
				if !seen[d] {
					seen[d] = true
					deps = append(deps, d)
				}
			}
			err := e.Commit(in, seq, deps)
			if err == nil {
				if _, ok := model[in]; !ok {
					canon, _ := canonicalDeps(deps)
					model[in] = &mrec{seq: seq, deps: canon}
				}
				commits = append(commits, fmt.Sprintf("%v seq=%d deps=%v", in, seq, fmtInsts(deps)))
			}
			if rng.Intn(3) == 0 {
				check()
			}
		}
		t.Logf("seed=%d N=%d 输入提交: %s", seed, n, strings.Join(commits, "; "))
		check()
	}
}

// 相同操作序列重放得到完全相同的序列与错误。
func TestReplayDeterminism(t *testing.T) {
	type op struct {
		in   Instance
		seq  int
		deps []Instance
	}
	script := []op{
		{inst(0, 1), 3, []Instance{inst(1, 1)}},
		{inst(1, 1), 1, nil},
		{inst(0, 1), 3, []Instance{inst(1, 1)}},             // 空操作
		{inst(0, 1), 4, nil},                                // 冲突
		{inst(0, 2), 2, []Instance{inst(0, 1), inst(0, 2)}}, // 自依赖
		{inst(0, 2), 2, []Instance{inst(0, 1)}},
		{inst(1, 2), 5, []Instance{inst(0, 2), inst(1, 1)}},
	}
	run := func() ([]string, [][]Instance) {
		e := mustNew(t, 2, 8)
		var errs []string
		var execs [][]Instance
		for _, o := range script {
			err := e.Commit(o.in, o.seq, o.deps)
			errs = append(errs, fmt.Sprint(err))
			execs = append(execs, e.Execute())
		}
		return errs, execs
	}
	errs1, execs1 := run()
	for i := 0; i < 5; i++ {
		errs2, execs2 := run()
		if !reflect.DeepEqual(errs1, errs2) || !reflect.DeepEqual(execs1, execs2) {
			t.Fatalf("replay %d diverged:\n%v %v\n%v %v", i, errs1, execs1, errs2, execs2)
		}
	}
	t.Logf("重放错误序列: %v", errs1)
	t.Logf("重放执行序列: %v", execs1)
}

// 并发调用：结果等价于某个串行顺序，每个实例恰好执行一次。
func TestConcurrentUse(t *testing.T) {
	const n = 4
	e := mustNew(t, n, 1000)
	var wg sync.WaitGroup
	var mu sync.Mutex
	executed := make(map[Instance]int)

	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for k := 0; k < 25; k++ {
				in := inst(w%n, w*25+k+1)
				if err := e.Commit(in, 1+k, nil); err != nil {
					t.Errorf("Commit(%v): %v", in, err)
					return
				}
				for _, got := range e.Execute() {
					mu.Lock()
					executed[got]++
					mu.Unlock()
				}
				_ = e.Pending()
			}
		}(w)
	}
	wg.Wait()
	for {
		got := e.Execute()
		if len(got) == 0 {
			break
		}
		for _, in := range got {
			executed[in]++
		}
	}
	if len(executed) != 200 {
		t.Fatalf("executed %d distinct instances, want 200", len(executed))
	}
	for in, c := range executed {
		if c != 1 {
			t.Fatalf("%v executed %d times", in, c)
		}
	}
	if got := e.Pending(); len(got) != 0 {
		t.Fatalf("Pending() = %v, want empty", got)
	}
}
