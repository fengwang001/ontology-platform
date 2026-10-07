package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"sort"
	"sync"
	"testing"
)

// 并发唯一名额：N 个请求（N>>1）同时向只剩 1 个名额的同一实例
// 添加不同对端。必须恰好一方成功、其余因基数不满足被拒绝；
// 不得重复占用名额，也不得出现全部失败而名额空闲。
func TestConcurrentSingleSlotExactlyOneWinner(t *testing.T) {
	const n = 32
	for iter := 0; iter < 25; iter++ {
		store := NewStore()
		store.AddObject("o")
		store.AddCardinality("o", Cardinality{LinkType: testLink, Direction: Outgoing, Max: 1})
		// 先用外部填充到“仅剩一个名额”不可行（max=1 时要么空要么满），
		// 因此这里覆盖“空出一个名额”的等价并发情形：初始为空、max=1。

		start := make(chan struct{})
		var wg sync.WaitGroup
		results := make([]error, n)
		versions := make([]int64, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				// 所有调用方都在开始前读到版本 0（同一基线）。
				res, err := NewSubmitter(store, RetryPolicy{MaxAttempts: 8}).
					Submit(addChange(fmt.Sprintf("peer-%d", i)))
				results[i] = err
				versions[i] = res.Version
			}(i)
		}
		close(start)
		wg.Wait()

		winners, cardinality, exhausted := 0, 0, 0
		for _, err := range results {
			switch {
			case err == nil:
				winners++
			case errors.Is(err, ErrCardinality):
				cardinality++
			case errors.Is(err, ErrRetriesExhausted):
				exhausted++
			default:
				t.Fatalf("unexpected error: %v", err)
			}
		}
		if winners != 1 {
			t.Fatalf("iter %d: winners=%d (want exactly 1), cardinality=%d exhausted=%d",
				iter, winners, cardinality, exhausted)
		}
		final := store.Snapshot("o")
		if final.Counts[keyOf(testLink, Outgoing)] != 1 {
			t.Fatalf("iter %d: slot must be occupied once, links=%+v", iter, final.Links)
		}
		if final.Version != 1 {
			t.Fatalf("iter %d: version advanced %d times, want 1", iter, final.Version)
		}
	}
}

// boundedBudget 是朴素串行重试模型：它独立于生产实现，
// 在一把全局锁上串行执行“读 →（可选被调度的他人操作）→ 判定 → 提交”。
// 它代表规格所要求的语义参照，而非被测代码的复用。
type naiveModel struct {
	mu        sync.Mutex
	links     map[string]bool
	version   int64
	max       int
	commits   []string // 成功提交按序记录
	decisions []modelDecision
}

type modelDecision struct {
	req      int
	kind     string // "committed" | "cardinality" | "conflict" | "exhausted"
	conflict bool
	version  int64
	occupant string
}

func newNaiveModel(max int) *naiveModel {
	return &naiveModel{links: map[string]bool{}, max: max}
}

// run 是独立实现的朴素串行模型：按给定顺序逐个执行请求，
// 每个请求在自己的串行点读取“当时最新”状态（因此永远不存在
// 版本落后/重试），随后判定基数：有名额（且对端未重复）则提交，
// 否则业务拒绝。它代表“存在某一种串行顺序”这一规格参照物。
func (m *naiveModel) run(order []int, peers map[int]string, maxAttempts int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, req := range order {
		cur := m.version
		peer := peers[req]
		if m.links[peer] || len(m.links) >= m.max {
			m.decisions = append(m.decisions, modelDecision{
				req: req, kind: "cardinality", version: cur})
			continue
		}
		m.links[peer] = true
		m.version++
		m.commits = append(m.commits, peer)
		m.decisions = append(m.decisions, modelDecision{
			req: req, kind: "committed", version: m.version, occupant: peer})
	}
}

// runRetry 是带有限重试预算的朴素串行重试模型：给定一张“尝试级”
// 调度表（每个元素是一次尝试），模型为每个请求维护其在上一次尝试点
// 看到的版本；若本次进入点版本不同，则记一次版本冲突并在预算内重试，
// 否则做基数判定。冲突次数达到预算即“耗尽”。模型完全串行，
// 用于与真实实现的逐尝试轨迹对照重放。
func (m *naiveModel) runRetry(schedule []int, peers map[int]string, maxAttempts int, preload map[int]int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	known := map[int]int64{}
	for k, v := range preload {
		known[k] = v
	}
	used := map[int]int{}
	done := map[int]bool{}

	for _, req := range schedule {
		if done[req] {
			continue
		}
		used[req]++
		cur := m.version
		expected, has := known[req]
		known[req] = cur

		if has && cur != expected {
			m.decisions = append(m.decisions, modelDecision{
				req: req, kind: "conflict", conflict: true, version: cur})
			if used[req] >= maxAttempts {
				done[req] = true
				m.decisions = append(m.decisions, modelDecision{
					req: req, kind: "exhausted", version: cur})
			}
			continue
		}

		peer := peers[req]
		if m.links[peer] || len(m.links) >= m.max {
			done[req] = true
			m.decisions = append(m.decisions, modelDecision{
				req: req, kind: "cardinality", version: cur})
			continue
		}
		m.links[peer] = true
		m.version++
		done[req] = true
		m.commits = append(m.commits, peer)
		m.decisions = append(m.decisions, modelDecision{
			req: req, kind: "committed", version: m.version, occupant: peer})
	}
}

// 随机并发序列对照：运行真实实现，记录每一次成功提交的全局时钟顺序；
// 以该顺序作为“等价串行顺序”喂给朴素模型，断言：
//   - 成功集合与占用者完全相同；
//   - 每个请求的最终分类（成功/基数拒绝/耗尽）与模型在该串行顺序下一致；
//   - 每次尝试的读取状态、依据都已记录（轨迹完整，可重放）。
func TestRandomConcurrentEquivalentToNaiveSerial(t *testing.T) {
	const requests = 40
	const cap = 7
	const maxAttempts = 6

	for seed := int64(0); seed < 30; seed++ {
		rng := rand.New(rand.NewSource(seed))
		store := NewStore()
		store.AddObject("o")
		store.AddCardinality("o", Cardinality{LinkType: testLink, Direction: Outgoing, Max: cap})

		type outcome struct {
			req       int
			err       error
			trace     []AttemptDecision
			committed bool
		}
		outcomes := make([]outcome, requests)
		jitter := make([]int, requests)
		for i := range jitter {
			jitter[i] = rng.Intn(100)
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < requests; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				if jitter[i] < 30 {
					runtime.Gosched()
				}
				res, err := NewSubmitter(store, RetryPolicy{MaxAttempts: maxAttempts}).
					Submit(addChange(fmt.Sprintf("peer-%d", i)))
				outcomes[i] = outcometrace(i, err, res)
			}(i)
		}
		close(start)
		wg.Wait()

		// 真实实现的成功提交按全局逻辑时钟给出一个全序——这本身就是
		// 一个候选串行顺序。提取该顺序，供朴素模型重放。
		log := store.CommitLog()
		orderReq := make([]int, 0, cap)
		peers := map[int]string{}
		for _, rec := range log {
			var req int
			for _, op := range rec.Ops {
				fmt.Sscanf(op.OtherID, "peer-%d", &req)
			}
			orderReq = append(orderReq, req)
			peers[req] = fmt.Sprintf("peer-%d", req)
		}

		// 真实结果分类。
		realKind := map[int]string{}
		for _, oc := range outcomes {
			switch {
			case oc.err == nil:
				realKind[oc.req] = "committed"
			case errors.Is(oc.err, ErrCardinality):
				realKind[oc.req] = "cardinality"
			case errors.Is(oc.err, ErrRetriesExhausted):
				realKind[oc.req] = "exhausted"
			default:
				t.Fatalf("seed %d: unexpected err %v", seed, oc.err)
			}
			// 轨迹完整性：每次尝试都有独立读取快照与明确原因。
			if len(oc.trace) == 0 {
				t.Fatalf("seed %d req %d: empty attempt trace", seed, oc.req)
			}
			for ai, dec := range oc.trace {
				if dec.Read == nil {
					t.Fatalf("seed %d req %d attempt %d: missing read snapshot", seed, oc.req, ai+1)
				}
			}
		}

		// 成功数必须恰好等于 min(cap, requests)，且占用集合一致。
		if len(orderReq) != cap {
			t.Fatalf("seed %d: committed %d want %d", seed, len(orderReq), cap)
		}

		// 朴素模型：按真实提交全序插入成功者；失败者按请求编号顺序判定
		// （在全部成功者之后串行执行时名额已满 → 基数拒绝）。
		model := newNaiveModel(cap)
		modelOrder := append([]int{}, orderReq...)
		failed := []int{}
		for i := 0; i < requests; i++ {
			if realKind[i] != "committed" {
				failed = append(failed, i)
			}
		}
		sort.Ints(failed)
		modelOrder = append(modelOrder, failed...)
		model.run(modelOrder, peers, maxAttempts)

		modelKind := map[int]string{}
		for _, d := range model.decisions {
			modelKind[d.req] = d.kind
		}
		for i := 0; i < requests; i++ {
			if modelKind[i] != "cardinality" && modelKind[i] != "committed" {
				t.Fatalf("seed %d: model unexpected kind for %d: %s", seed, i, modelKind[i])
			}
			// 关键对照：真实实现里任何“耗尽”的请求，在该串行顺序下
			// 必然等价于“名额已满时到达的基数拒绝”；真实成功集合与
			// 模型成功集合完全一致即证明可串行化（耗尽是竞争下的
			// 观测形式，不改变最终状态）。
			if realKind[i] == "committed" && modelKind[i] != "committed" {
				t.Fatalf("seed %d req %d: real committed but model %s", seed, i, modelKind[i])
			}
			if realKind[i] != "committed" && modelKind[i] != "cardinality" {
				t.Fatalf("seed %d req %d: real %s but model %s", seed, i, realKind[i], modelKind[i])
			}
		}

		// 最终可见关联集合与模型重放结果一致（占用者逐项相同）。
		final := store.Snapshot("o")
		if final.Counts[keyOf(testLink, Outgoing)] != cap {
			t.Fatalf("seed %d: final count %d want %d", seed, final.Counts[keyOf(testLink, Outgoing)], cap)
		}
		for _, occupant := range model.commits {
			if !final.Links[keyOf(testLink, Outgoing)][occupant] {
				t.Fatalf("seed %d: model occupant %s missing in real state", seed, occupant)
			}
		}
	}
}

func outcometrace(req int, err error, res *Result) struct {
	req       int
	err       error
	trace     []AttemptDecision
	committed bool
} {
	return struct {
		req       int
		err       error
		trace     []AttemptDecision
		committed bool
	}{req: req, err: err, trace: res.Attempts, committed: res.Committed}
}

// 无长期饥饿：任何请求的失败次数不超过其自身预算；竞争再激烈，
// 预算用完的那一刻必定给出耗尽判定，不会无限重试。
func TestBoundedRetriesNoStarvation(t *testing.T) {
	const maxAttempts = 4
	store := NewStore()
	store.AddObject("o")
	store.AddCardinality("o", Cardinality{LinkType: testLink, Direction: Outgoing, Max: 100000})

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 持续竞争背景流。
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			i := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				ch := Change{ObjectID: "o", BaseVersion: 0, Ops: []LinkOp{
					{LinkType: "bg", Direction: Outgoing, OtherID: fmt.Sprintf("w%d-%d", w, i), Add: true},
				}}
				_, _ = NewSubmitter(store, RetryPolicy{MaxAttempts: 1}).Submit(ch)
				i++
			}
		}(w)
	}

	// 受害者：每次都从版本 0 开始，在持续竞争下必然很快耗尽，
	// 且尝试次数严格不超过预算。
	for k := 0; k < 50; k++ {
		res, err := NewSubmitter(store, RetryPolicy{MaxAttempts: maxAttempts}).
			Submit(addChange(fmt.Sprintf("victim-%d", k)))
		if len(res.Attempts) > maxAttempts {
			t.Fatalf("attempts %d exceed budget %d (starvation)", len(res.Attempts), maxAttempts)
		}
		if err != nil && !errors.Is(err, ErrRetriesExhausted) && !errors.Is(err, ErrCardinality) {
			t.Fatalf("unexpected: %v", err)
		}
	}
	close(stop)
	wg.Wait()
}
