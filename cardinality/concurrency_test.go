package cardinality

import (
	"fmt"
	"sort"
	"sync"
	"testing"
)

// naiveEngine 是一份独立编写的朴素串行实现：
// 直接按给定操作序列逐条执行，不共享产品代码的任何内部状态，
// 只复刻“按 (seq=登记顺序,id) 全序 + 当前容量”这一规格。
type naiveEngine struct {
	limit    int
	seq      int
	links    map[string]int // id -> 登记序号
	state    map[string]LinkState
	retained map[string]bool
	// markBatch 记录链接被标记为超额的批次序号；
	// 恢复顺序必须是标记顺序的逆序（批次新的先恢复）。
	markBatch map[string]int
	batchSeq  int
}

func newNaive() *naiveEngine {
	return &naiveEngine{
		limit:     Unlimited,
		links:     make(map[string]int),
		state:     make(map[string]LinkState),
		retained:  make(map[string]bool),
		markBatch: make(map[string]int),
	}
}

func (n *naiveEngine) ordered() []string {
	ids := make([]string, 0, len(n.links))
	for id := range n.links {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if n.links[ids[i]] != n.links[ids[j]] {
			return n.links[ids[i]] < n.links[ids[j]]
		}
		return ids[i] < ids[j]
	})
	return ids
}

func (n *naiveEngine) capacity(ordered []string) int {
	c := n.limit
	if len(n.retained) > 0 {
		last := 0
		for i, id := range ordered {
			if n.retained[id] {
				last = i + 1
			}
		}
		if last > c {
			c = last
		}
	}
	return c
}

// reconcile 按规格全量对账，返回标记与恢复的 ID 序列。
func (n *naiveEngine) reconcile() (marked, restored []string) {
	ordered := n.ordered()
	cap := n.capacity(ordered)
	n.batchSeq++
	batch := n.batchSeq
	type rp struct {
		id    string
		rank  int
		batch int
	}
	var toRestore []rp
	for i, id := range ordered {
		st := n.state[id]
		switch {
		case i >= cap && st == StateActive:
			n.state[id] = StatePending
			n.markBatch[id] = batch
			marked = append(marked, id)
		case i < cap && st == StatePending:
			toRestore = append(toRestore, rp{id, i + 1, n.markBatch[id]})
		}
	}
	sort.SliceStable(toRestore, func(i, j int) bool {
		if toRestore[i].batch != toRestore[j].batch {
			return toRestore[i].batch > toRestore[j].batch
		}
		return toRestore[i].rank > toRestore[j].rank
	})
	for _, item := range toRestore {
		n.state[item.id] = StateActive
		delete(n.markBatch, item.id)
		restored = append(restored, item.id)
	}
	return marked, restored
}

func (n *naiveEngine) setLimit(limit int) (marked, restored []string) {
	n.limit = limit
	return n.reconcile()
}

func (n *naiveEngine) create(id string) bool {
	ordered := n.ordered()
	active := 0
	for _, x := range ordered {
		if n.state[x] == StateActive {
			active++
		}
	}
	if active >= n.capacity(ordered) {
		return false
	}
	n.seq++
	n.links[id] = n.seq
	n.state[id] = StateActive
	return true
}

func (n *naiveEngine) finalize(id string, disp Disposition) bool {
	if n.state[id] != StatePending {
		return false
	}
	if disp == DispositionRetain {
		n.retained[id] = true
		n.reconcile()
		return true
	}
	delete(n.links, id)
	delete(n.state, id)
	delete(n.retained, id)
	n.reconcile()
	return true
}

type recordedOp struct {
	kind     string // "limit" | "create"
	limit    int
	id       string
	accepted bool
	marked   []string
	restored []string
}

// TestConcurrentInterleavingMatchesSerialTotalOrder 并发下调与创建交织时，
// 以实际全序（OrderSeq）重放到独立朴素串行实现，接受/拒绝与超额标记必须一致。
func TestConcurrentInterleavingMatchesSerialTotalOrder(t *testing.T) {
	const workers = 8
	const perWorker = 60

	e := NewEngine(nil)
	k := keyOf("owns", "Z")

	// 预置 40 条有效链接。
	for i := 0; i < 40; i++ {
		r := e.CreateLink("owns", "Z", fmt.Sprintf("T-%d", i), fmt.Sprintf("Z-%04d", i))
		if !r.Accepted {
			t.Fatal(r.RejectReason)
		}
	}

	type opResult struct {
		op       recordedOp
		orderSeq int64
	}
	var mu sync.Mutex
	var actual []opResult

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				var rec recordedOp
				var seq int64
				if (w+i)%3 == 0 {
					limits := []int{5, 30, 12, 60, 20, 40}
					lim := limits[(w*7+i*3)%len(limits)]
					res := e.SetLimit(k, lim)
					rec = recordedOp{kind: "limit", limit: lim, marked: res.Marked, restored: res.Restored}
					seq = res.OrderSeq
				} else {
					id := fmt.Sprintf("Z-w%d-%02d", w, i)
					res := e.CreateLink("owns", "Z", "T-"+id, id)
					rec = recordedOp{kind: "create", id: id, accepted: res.Accepted}
					seq = res.OrderSeq
				}
				mu.Lock()
				actual = append(actual, opResult{op: rec, orderSeq: seq})
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()

	// 以引擎给出的全局全序序号排序——这是锁实际交出的串行顺序。
	sort.Slice(actual, func(i, j int) bool { return actual[i].orderSeq < actual[j].orderSeq })

	// 用同一份初始状态 + 同一条全序在朴素实现上重放。
	nav := newNaive()
	for i := 0; i < 40; i++ {
		nav.create(fmt.Sprintf("Z-%04d", i))
	}

	for _, got := range actual {
		switch got.op.kind {
		case "limit":
			m, r := nav.setLimit(got.op.limit)
			if !equalStrings(m, got.op.marked) || !equalStrings(r, got.op.restored) {
				t.Fatalf("at seq %d limit=%d naive marked=%v restored=%v vs actual marked=%v restored=%v",
					got.orderSeq, got.op.limit, m, r, got.op.marked, got.op.restored)
			}
		case "create":
			wantAccepted := nav.create(got.op.id)
			if wantAccepted != got.op.accepted {
				t.Fatalf("at seq %d create %s: naive accepted=%v actual=%v",
					got.orderSeq, got.op.id, wantAccepted, got.op.accepted)
			}
		}
	}

	// 终态比对：active / pending / 登记数 / 有效容量全部一致。
	var navActive, navPending []string
	for _, id := range nav.ordered() {
		switch nav.state[id] {
		case StateActive:
			navActive = append(navActive, id)
		case StatePending:
			navPending = append(navPending, id)
		}
	}
	if got := idsOf(e.ActiveLinks(k)); !equalStrings(got, navActive) {
		t.Fatalf("final active mismatch: %v vs %v", got, navActive)
	}
	if got := pendingIDs(e.PendingLinks(k)); !equalStrings(got, navPending) {
		t.Fatalf("final pending mismatch: %v vs %v", got, navPending)
	}
	if e.RegisteredCount(k) != len(nav.links) {
		t.Fatalf("final registered mismatch: %d vs %d", e.RegisteredCount(k), len(nav.links))
	}
	if e.EffectiveCapacity(k) != nav.capacity(nav.ordered()) {
		t.Fatalf("final capacity mismatch: %d vs %d", e.EffectiveCapacity(k), nav.capacity(nav.ordered()))
	}

	// 不允许出现“按旧上限接受、下调却声称先生效”的矛盾：
	// 每个被接受的创建在其全序位置上必须能被朴素实现接受（已在上面逐条证明），
	// 这里再核对全序序号严格递增且唯一。
	seen := map[int64]bool{}
	for _, item := range actual {
		if seen[item.orderSeq] {
			t.Fatalf("duplicate order seq %d", item.orderSeq)
		}
		seen[item.orderSeq] = true
	}
}
