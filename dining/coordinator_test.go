package dining

import (
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// fifoNetwork 注入的消息网络：同一有向信道 FIFO，不同信道可任意交错。
type fifoNetwork struct {
	mu     sync.Mutex
	queues map[[2]int][]Message
	sent   int
}

func newFIFONetwork() *fifoNetwork {
	return &fifoNetwork{queues: make(map[[2]int][]Message)}
}

func (n *fifoNetwork) Send(m Message) {
	n.mu.Lock()
	defer n.mu.Unlock()
	ch := [2]int{m.From, m.To}
	n.queues[ch] = append(n.queues[ch], m)
	n.sent++
}

func (n *fifoNetwork) channels() [][2]int {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out [][2]int
	for ch, q := range n.queues {
		if len(q) > 0 {
			out = append(out, ch)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

func (n *fifoNetwork) front(ch [2]int) (Message, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	q := n.queues[ch]
	if len(q) == 0 {
		return Message{}, false
	}
	return q[0], true
}

func (n *fifoNetwork) pop(ch [2]int) (Message, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	q := n.queues[ch]
	if len(q) == 0 {
		return Message{}, false
	}
	m := q[0]
	n.queues[ch] = q[1:]
	return m, true
}

// popAny 以确定顺序弹出某个非空信道的队首消息。
func (n *fifoNetwork) popAny() (Message, bool) {
	for _, ch := range n.channels() {
		if m, ok := n.pop(ch); ok {
			return m, true
		}
	}
	return Message{}, false
}

func ringEdges(n int) [][2]int {
	var edges [][2]int
	for i := 0; i < n; i++ {
		edges = append(edges, [2]int{i, (i + 1) % n})
	}
	return edges
}

func completeEdges(n int) [][2]int {
	var edges [][2]int
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			edges = append(edges, [2]int{i, j})
		}
	}
	return edges
}

func holdsAllForks(snap Snapshot, p int) bool {
	for e, f := range snap.Forks {
		if (e.Lo == p || e.Hi == p) && (f.InFlight || f.Holder != p) {
			return false
		}
	}
	return true
}

func minMeals(meals []int) int {
	m := meals[0]
	for _, v := range meals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustInvariant(t *testing.T, c *Coordinator) {
	t.Helper()
	if err := c.CheckInvariants(); err != nil {
		t.Fatalf("invariant violated: %v", err)
	}
}

// 初始定向：叉在标识较小端且为脏，令牌在另一端，优先关系按 id 升序定向。
func TestInitialOrientation(t *testing.T) {
	n := 5
	edges := ringEdges(n)
	c, err := NewCoordinator(n, edges, nil)
	must(t, err)
	snap := c.Snapshot()
	for _, pair := range edges {
		e := Edge{Lo: min(pair[0], pair[1]), Hi: max(pair[0], pair[1])}
		f := snap.Forks[e]
		tk := snap.Tokens[e]
		t.Logf("edge %v: fork@%d dirty=%v, token@%d", e, f.Holder, f.Dirty, tk.Holder)
		if f.Holder != e.Lo || !f.Dirty || f.InFlight {
			t.Errorf("edge %v: fork should be dirty at smaller id %d, got %+v", e, e.Lo, f)
		}
		if tk.Holder != e.Hi || tk.InFlight {
			t.Errorf("edge %v: token should be at larger id %d, got %+v", e, e.Hi, tk)
		}
	}
	for e, d := range snap.Orientation() {
		if d[0] != e.Lo || d[1] != e.Hi {
			t.Errorf("edge %v: initial orientation should be %d->%d, got %d->%d", e, e.Lo, e.Hi, d[0], d[1])
		}
	}
	if !snap.Acyclic() {
		t.Error("initial orientation must be acyclic")
	}
	t.Log("判定依据: 叉脏时优先关系由持有者指向对方, 初始全部按 id 升序定向, 故无环")
}

// 脏叉被请求时让出，且饥饿的让出方立即用同一枚令牌再请求。
func TestDirtyForkReleaseAndImmediateReRequest(t *testing.T) {
	net := newFIFONetwork()
	c, err := NewCoordinator(2, [][2]int{{0, 1}}, net)
	must(t, err)
	e := Edge{Lo: 0, Hi: 1}

	must(t, c.Hungry(0))
	t.Log("输入: Hungry(0); 0 持有全部叉 -> 输出: 无消息")
	if net.sent != 0 {
		t.Fatalf("fork holder should send nothing, sent %d", net.sent)
	}
	must(t, c.Hungry(1))
	m, ok := net.pop([2]int{1, 0})
	t.Logf("输入: Hungry(1); 1 缺叉且持令牌 -> 输出: %s", m)
	if !ok || m.Kind != Request {
		t.Fatalf("expected request 1->0, got %v ok=%v", m, ok)
	}

	must(t, c.Deliver(m.ID))
	snap := c.Snapshot()
	f, tk := snap.Forks[e], snap.Tokens[e]
	t.Logf("输入: 投递请求到0(饥饿/叉脏/未进餐) -> 输出: fork=%+v token=%+v", f, tk)
	if !f.InFlight || f.To != 1 {
		t.Fatalf("dirty fork should be cleaned and sent to 1, got %+v", f)
	}
	if !tk.InFlight || tk.To != 1 {
		t.Fatalf("still-hungry 0 should immediately re-request with the token, got %+v", tk)
	}
	t.Log("判定依据: 脏叉未进餐则洗净发出; 仍饥饿则立即用该令牌再请求")

	mf, ok := net.pop([2]int{0, 1})
	if !ok || mf.Kind != ForkMsg {
		t.Fatalf("FIFO: fork should be first on 0->1, got %v", mf)
	}
	must(t, c.Deliver(mf.ID))
	if got := c.Snapshot().Forks[e]; got.Holder != 1 || got.Dirty {
		t.Fatalf("received fork should be clean at 1, got %+v", got)
	}
	must(t, c.Eat(1))
	t.Log("输入: Eat(1); 1 持有全部叉 -> 进餐成功")

	mr, ok := net.pop([2]int{0, 1})
	if !ok || mr.Kind != Request {
		t.Fatalf("expected re-request 0->1, got %v", mr)
	}
	must(t, c.Deliver(mr.ID))
	snap = c.Snapshot()
	if !snap.Deferred[e] || snap.Forks[e].InFlight {
		t.Fatalf("request during eating must be deferred, got deferred=%v fork=%+v", snap.Deferred[e], snap.Forks[e])
	}
	t.Log("输入: 进餐中收到请求 -> 输出: 暂存请求, 不发叉")

	must(t, c.Finish(1))
	snap = c.Snapshot()
	if snap.Deferred[e] || !snap.Forks[e].InFlight || snap.Forks[e].To != 0 {
		t.Fatalf("finish should satisfy deferred request, got deferred=%v fork=%+v", snap.Deferred[e], snap.Forks[e])
	}
	t.Log("输入: Finish(1) -> 输出: 叉变脏后立即洗净发给0满足暂存请求")

	mf2, ok := net.pop([2]int{1, 0})
	if !ok || mf2.Kind != ForkMsg {
		t.Fatalf("expected fork 1->0, got %v", mf2)
	}
	must(t, c.Deliver(mf2.ID))
	must(t, c.Eat(0))
	t.Log("0 收到净叉后持有全部叉 -> Eat(0) 成功")
	mustInvariant(t, c)
}

// 净叉持有者（饥饿、尚未进餐）收到请求时暂存，进餐结束后才发出。
func TestCleanForkDefersRequest(t *testing.T) {
	net := newFIFONetwork()
	c, err := NewCoordinator(2, [][2]int{{0, 1}}, net)
	must(t, err)
	e := Edge{Lo: 0, Hi: 1}
	must(t, c.Hungry(0))
	must(t, c.Hungry(1))
	m, _ := net.pop([2]int{1, 0})
	must(t, c.Deliver(m.ID)) // 0 让出脏叉并立即再请求
	mf, _ := net.pop([2]int{0, 1})
	must(t, c.Deliver(mf.ID)) // 1 持有净叉, 饥饿未进餐
	mr, _ := net.pop([2]int{0, 1})
	must(t, c.Deliver(mr.ID)) // 0 的再请求到达净叉持有者
	snap := c.Snapshot()
	t.Logf("输入: 净叉持有者(饥饿)收到请求 -> 输出: deferred=%v forkInFlight=%v", snap.Deferred[e], snap.Forks[e].InFlight)
	if !snap.Deferred[e] {
		t.Fatal("clean fork holder must defer the request")
	}
	if snap.Forks[e].InFlight {
		t.Fatal("clean fork must not be sent before eating")
	}
	must(t, c.Eat(1))
	must(t, c.Finish(1))
	snap = c.Snapshot()
	if f := snap.Forks[e]; !f.InFlight || f.To != 0 {
		t.Fatalf("after finish the deferred request must be satisfied, got %+v", f)
	}
	t.Log("判定依据: 净叉暂存请求; 进餐结束叉变脏后立即洗净发出")
	mustInvariant(t, c)
}

// 相邻进程从不同时进餐：一条边只有一把叉，缺叉进程无法进餐。
func TestAdjacentCannotEatSimultaneously(t *testing.T) {
	net := newFIFONetwork()
	c, err := NewCoordinator(2, [][2]int{{0, 1}}, net)
	must(t, err)
	must(t, c.Hungry(0))
	must(t, c.Hungry(1))
	m, _ := net.pop([2]int{1, 0})
	must(t, c.Deliver(m.ID))
	mf, _ := net.pop([2]int{0, 1})
	must(t, c.Deliver(mf.ID))
	must(t, c.Eat(1))
	if err := c.Eat(0); !errors.Is(err, ErrMissingForks) {
		t.Fatalf("0 must not eat while 1 holds the fork, got %v", err)
	}
	if pairs := c.Snapshot().AdjacentEatingPairs(); len(pairs) != 0 {
		t.Fatalf("adjacent eating pairs: %v", pairs)
	}
	t.Log("判定依据: 每条边恰一把叉且进餐须持有全部叉 -> 相邻进程从不同时进餐")
	mustInvariant(t, c)
}

// drive 确定性驱动：投递消息、让持有全部叉的饥饿进程进餐并结束，
// 直到每个进程都进餐 targetMeals 次；每步校验不变量。
func drive(t *testing.T, c *Coordinator, net *fifoNetwork, np, targetMeals, maxSteps int) []int {
	t.Helper()
	meals := make([]int, np)
	for step := 0; step < maxSteps; step++ {
		if minMeals(meals) >= targetMeals {
			return meals
		}
		progress := false
		if m, ok := net.popAny(); ok {
			must(t, c.Deliver(m.ID))
			progress = true
		}
		snap := c.Snapshot()
		for p := 0; p < np; p++ {
			if snap.States[p] == Hungry && holdsAllForks(snap, p) {
				must(t, c.Eat(p))
				meals[p]++
				must(t, c.Finish(p))
				progress = true
			}
		}
		if !progress {
			made := false
			for p := 0; p < np; p++ {
				if snap.States[p] == Thinking {
					must(t, c.Hungry(p))
					made = true
				}
			}
			if !made {
				t.Fatalf("deadlock at step %d: states=%v", step, snap.States)
			}
		}
		mustInvariant(t, c)
	}
	t.Fatalf("starvation: meals=%v after %d steps", meals, maxSteps)
	return nil
}

// 环形与完全图：所有进程轮流进餐多轮，无死锁、无相邻同时进餐、优先关系无环。
func TestRingAndCompleteGraphs(t *testing.T) {
	cases := []struct {
		name  string
		n     int
		edges [][2]int
	}{
		{"ring6", 6, ringEdges(6)},
		{"complete5", 5, completeEdges(5)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			net := newFIFONetwork()
			c, err := NewCoordinator(tc.n, tc.edges, net)
			must(t, err)
			for p := 0; p < tc.n; p++ {
				must(t, c.Hungry(p))
			}
			meals := drive(t, c, net, tc.n, 3, 100000)
			t.Logf("%s: 每进程进餐次数=%v", tc.name, meals)
			t.Log("判定依据: 每步 CheckInvariants (叉/令牌唯一 + 无相邻同时进餐 + 无环), 且所有进程均完成进餐")
		})
	}
}

// runRandom 随机交错驱动：每步从合法操作（饥饿/进餐/结束/投递某信道队首）中
// 均匀随机选一个执行，直到每个进程都进餐 targetMeals 次。
// 每步校验不变量，并校验超车界限：进程 p 的请求被叉的持有者 q 接受后，
// q 在 p 进餐前至多再进餐一次。
func runRandom(t *testing.T, seed int64, np int, edges [][2]int, targetMeals, maxSteps int) (Snapshot, []int, int) {
	t.Helper()
	net := newFIFONetwork()
	c, err := NewCoordinator(np, edges, net)
	must(t, err)
	rng := rand.New(rand.NewSource(seed))
	meals := make([]int, np)
	pending := make(map[[2]int]int) // (请求者,持有者) -> 接受请求时持有者的进餐次数
	step := 0
	for ; step < maxSteps; step++ {
		if minMeals(meals) >= targetMeals {
			break
		}
		snap := c.Snapshot()
		type action struct {
			kind string
			p    int
			ch   [2]int
			m    Message
		}
		var acts []action
		for p := 0; p < np; p++ {
			switch snap.States[p] {
			case Thinking:
				acts = append(acts, action{kind: "hungry", p: p})
			case Hungry:
				if holdsAllForks(snap, p) {
					acts = append(acts, action{kind: "eat", p: p})
				}
			case Eating:
				acts = append(acts, action{kind: "finish", p: p})
			}
		}
		for _, ch := range net.channels() {
			if m, ok := net.front(ch); ok {
				acts = append(acts, action{kind: "deliver", ch: ch, m: m})
			}
		}
		if len(acts) == 0 {
			t.Fatalf("deadlock at step %d: states=%v", step, snap.States)
		}
		a := acts[rng.Intn(len(acts))]
		var aerr error
		switch a.kind {
		case "hungry":
			aerr = c.Hungry(a.p)
		case "eat":
			for pq, rec := range pending {
				if pq[1] == a.p && meals[a.p] > rec {
					t.Fatalf("overtaking bound violated: holder %d would eat %d-th time since request of %d (recorded at %d)",
						a.p, meals[a.p]+1, pq[0], rec)
				}
			}
			aerr = c.Eat(a.p)
			meals[a.p]++
			for pq := range pending {
				if pq[0] == a.p {
					delete(pending, pq)
				}
			}
		case "finish":
			aerr = c.Finish(a.p)
		case "deliver":
			if _, ok := net.pop(a.ch); !ok {
				t.Fatalf("network queue %v empty at step %d", a.ch, step)
			}
			if a.m.Kind == Request {
				// 仅当接收者当前持有该叉（请求被持有者接受而非被转发）时记录。
				if f := snap.Forks[a.m.Edge]; !f.InFlight && f.Holder == a.m.To {
					pending[[2]int{a.m.From, a.m.To}] = meals[a.m.To]
				}
			}
			aerr = c.Deliver(a.m.ID)
		}
		must(t, aerr)
		mustInvariant(t, c)
	}
	if minMeals(meals) < targetMeals {
		t.Fatalf("starvation: meals=%v after %d steps", meals, maxSteps)
	}
	return c.Snapshot(), meals, step
}

// 千次随机交错：无相邻同时进餐、优先关系无环、超车至多一次、无进程饿死。
func TestRandomInterleaving(t *testing.T) {
	_, meals, steps := runRandom(t, 20260930, 6, completeEdges(6), 20, 2000000)
	if steps < 1000 {
		t.Fatalf("expected at least 1000 interleaved steps, got %d", steps)
	}
	t.Logf("输入: K6 + 固定种子随机交错驱动 -> 输出: steps=%d meals=%v", steps, meals)
	t.Log("判定依据: 千次随机交错中每步 CheckInvariants + 超车界限断言 + 所有进程均进餐>=20次(无饿死)")
}

// 相同的操作与投递序列重放结果相同。
func TestReplayDeterminism(t *testing.T) {
	s1, m1, st1 := runRandom(t, 924, 5, ringEdges(5), 4, 100000)
	s2, m2, st2 := runRandom(t, 924, 5, ringEdges(5), 4, 100000)
	if st1 != st2 || !reflect.DeepEqual(m1, m2) || !reflect.DeepEqual(s1, s2) {
		t.Fatalf("replay mismatch: steps %d vs %d, meals %v vs %v", st1, st2, m1, m2)
	}
	t.Logf("输入: 同一种子重放两次 -> 输出: steps=%d meals=%v 快照完全一致", st1, m1)
	t.Log("判定依据: 全部转移在单把互斥锁内顺序执行, 消息按 ID 单调分配")
}

// 各类非法操作整体拒绝且状态不变。
func TestRejectedOperations(t *testing.T) {
	if _, err := NewCoordinator(3, [][2]int{{0, 3}}, nil); !errors.Is(err, ErrUnknownProcess) {
		t.Errorf("bad endpoint: want %v, got %v", ErrUnknownProcess, err)
	}
	if _, err := NewCoordinator(3, [][2]int{{1, 1}}, nil); !errors.Is(err, ErrSelfLoop) {
		t.Errorf("self loop: want %v, got %v", ErrSelfLoop, err)
	}
	if _, err := NewCoordinator(3, [][2]int{{0, 1}, {1, 0}}, nil); !errors.Is(err, ErrDuplicateEdge) {
		t.Errorf("duplicate edge: want %v, got %v", ErrDuplicateEdge, err)
	}
	t.Log("构造期: 端点不存在/自环/重复边(含反向) 分别返回可区分错误")

	c, err := NewCoordinator(3, [][2]int{{0, 1}, {1, 2}}, nil)
	must(t, err)
	checkNoChange := func(name string, before Snapshot, f func() error, want error) {
		t.Helper()
		err := f()
		if !errors.Is(err, want) {
			t.Errorf("%s: want %v, got %v", name, want, err)
		}
		if after := c.Snapshot(); !reflect.DeepEqual(before, after) {
			t.Errorf("%s: rejected op changed state\nbefore=%+v\nafter=%+v", name, before, after)
		}
		t.Logf("输入: %s -> 输出: %v; 判定依据: 拒绝后快照与之前完全一致", name, err)
	}

	snap := c.Snapshot()
	checkNoChange("思考中进餐 Eat(0)", snap, func() error { return c.Eat(0) }, ErrNotHungry)
	checkNoChange("结束未在进餐的进程 Finish(0)", snap, func() error { return c.Finish(0) }, ErrNotEating)
	checkNoChange("未知进程 Hungry(9)", snap, func() error { return c.Hungry(9) }, ErrUnknownProcess)
	checkNoChange("投递不存在的消息 Deliver(999)", snap, func() error { return c.Deliver(999) }, ErrMessageNotFound)

	must(t, c.Hungry(1)) // 1 缺少边 {0,1} 上的叉
	snap = c.Snapshot()
	checkNoChange("缺叉进餐 Eat(1)", snap, func() error { return c.Eat(1) }, ErrMissingForks)
	checkNoChange("饥饿退回思考 Finish(1)", snap, func() error { return c.Finish(1) }, ErrHungryToThinking)
	checkNoChange("重复饥饿 Hungry(1)", snap, func() error { return c.Hungry(1) }, ErrNotThinking)
}

// 状态转移与投递可被并发调用。
func TestConcurrentAccess(t *testing.T) {
	np := 5
	net := newFIFONetwork()
	c, err := NewCoordinator(np, completeEdges(np), net)
	must(t, err)
	var wg sync.WaitGroup
	var once sync.Once
	var firstErr error
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 1000; i++ {
				snap := c.Snapshot()
				p := rng.Intn(np)
				switch snap.States[p] {
				case Thinking:
					_ = c.Hungry(p)
				case Hungry:
					_ = c.Eat(p)
				case Eating:
					_ = c.Finish(p)
				}
				if m, ok := net.popAny(); ok {
					_ = c.Deliver(m.ID)
				}
				if err := c.CheckInvariants(); err != nil {
					once.Do(func() { firstErr = err })
				}
			}
		}(int64(g))
	}
	wg.Wait()
	if firstErr != nil {
		t.Fatalf("invariant violated under concurrency: %v", firstErr)
	}
	mustInvariant(t, c)
	t.Log("输入: 8 goroutines x 1000 随机操作并发 -> 输出: 无数据竞争(-race)且不变量保持")
}
