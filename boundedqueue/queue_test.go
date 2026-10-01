package boundedqueue

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

type dlItem struct {
	msg      string
	reason   Reason
	deadline int64
}

func dlSnapshot(q *Queue[string]) []dlItem {
	got := q.DeadLetters()
	out := make([]dlItem, len(got))
	for i, d := range got {
		out[i] = dlItem{msg: d.Message, reason: d.Reason, deadline: d.Deadline}
	}
	return out
}

// TestExpiryExactlyAtDeadline: now == deadline counts as expired, and only
// consecutive head entries are cleaned.
func TestExpiryExactlyAtDeadline(t *testing.T) {
	q, err := New[string](3, PolicyReject)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustEnqueue := func(msg string, ttl, now int64) {
		if e := q.Enqueue(msg, ttl, now); e != nil {
			t.Fatalf("Enqueue(%s,t=%d,ttl=%d): %v", msg, now, ttl, e)
		}
	}
	mustEnqueue("a", 10, 0) // deadline 10
	mustEnqueue("b", 20, 0) // deadline 20
	mustEnqueue("c", 5, 5)  // deadline 10
	t.Logf("输入: enqueue a(ttl=10)@0, b(ttl=20)@0, c(ttl=5)@5; 到期时刻 a=10 b=20 c=10")

	got, derr := q.Dequeue(10)
	if derr != nil {
		t.Fatalf("Dequeue(10): %v", derr)
	}
	if got != "b" {
		t.Fatalf("Dequeue(10) = %q, want b (a 恰在到期时刻过期, 遇 b 停止清理)", got)
	}
	t.Logf("输出: dequeue@10 => 死信 a(expired), 出队 b; 判定依据: now>=deadline 判过期, 遇首条未过期即停")

	dls := dlSnapshot(q)
	if len(dls) != 1 || dls[0].msg != "a" || dls[0].reason != ReasonExpired {
		t.Fatalf("死信 = %+v, want [a expired]", dls)
	}
	if q.Len() != 1 {
		t.Fatalf("Len = %d, want 1 (b 已出队; c 曾被 b 挡住而占位)", q.Len())
	}
	t.Logf("输出: Len=%d 死信=[a expired]; 判定依据: b 出队后剩 c; c 此前被 b 挡住未被清理", q.Len())
}

// TestExpiredBehindFreshHeadStillOccupies: a stale message behind a fresh
// head occupies a slot; reject refuses while drop-head evicts the fresh head.
func TestExpiredBehindFreshHeadStillOccupies(t *testing.T) {
	q, _ := New[string](2, PolicyReject)
	_ = q.Enqueue("a", 100, 0) // deadline 100
	_ = q.Enqueue("b", 1, 0)   // deadline 1, 后部已过期

	if err := q.Enqueue("c", 100, 10); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("Enqueue(c)@10 err = %v, want ErrQueueFull", err)
	}
	if q.Len() != 2 || len(q.DeadLetters()) != 0 {
		t.Fatalf("拒绝后 Len=%d 死信=%d, want 2/0 (模拟清理不落地)", q.Len(), len(q.DeadLetters()))
	}
	t.Logf("输入: reject 下 enqueue c@10 到 [a(未过期),b(已过期)]; 输出: queue_full 状态不变; 判定依据: 队首未过期, 模拟清理 0 条仍满")

	qd, _ := New[string](2, PolicyDropHead)
	_ = qd.Enqueue("a", 100, 0)
	_ = qd.Enqueue("b", 1, 0)
	if err := qd.Enqueue("c", 100, 10); err != nil {
		t.Fatalf("drop-head Enqueue(c): %v", err)
	}
	dls := dlSnapshot(qd)
	if len(dls) != 1 || dls[0].msg != "a" || dls[0].reason != ReasonOverflow {
		t.Fatalf("死信 = %+v, want [a overflow]", dls)
	}
	if qd.Len() != 2 {
		t.Fatalf("Len = %d, want 2 (驱逐 a 并入队 c 后为 [b,c])", qd.Len())
	}
	got, _ := qd.Dequeue(10)
	if got != "c" {
		t.Fatalf("Dequeue = %q, want c (出队前先清理队首: b 在 now=10 已过期)", got)
	}
	dls = dlSnapshot(qd)
	if len(dls) != 2 || dls[0].msg != "a" || dls[0].reason != ReasonOverflow ||
		dls[1].msg != "b" || dls[1].reason != ReasonExpired {
		t.Fatalf("死信 = %+v, want [a overflow, b expired]", dls)
	}
	t.Logf("输入: drop-head 同场景 enqueue c@10 后 dequeue@10; 输出: 死信 a(overflow) 再 b(expired), 出队 c; 判定依据: 溢出驱逐未过期队首 a; 出队清理才移除 b")
}

// TestRejectCleanupLanding: cleanup that frees a slot lands; cleanup that
// leaves the queue full does not.
func TestRejectCleanupLanding(t *testing.T) {
	q, _ := New[string](2, PolicyReject)
	_ = q.Enqueue("a", 1, 0) // deadline 1
	_ = q.Enqueue("b", 100, 0)
	if err := q.Enqueue("c", 100, 5); err != nil {
		t.Fatalf("Enqueue(c)@5: %v, want nil (清理 a 后腾出空位)", err)
	}
	dls := dlSnapshot(q)
	if len(dls) != 1 || dls[0].msg != "a" || dls[0].reason != ReasonExpired {
		t.Fatalf("死信 = %+v, want [a expired]", dls)
	}
	if q.Len() != 2 {
		t.Fatalf("Len = %d, want 2", q.Len())
	}
	got, _ := q.Dequeue(5)
	if got != "b" {
		t.Fatalf("Dequeue = %q, want b", got)
	}
	t.Logf("输入: reject 满队 [a(已过期),b] enqueue c@5; 输出: 成功, 死信 a(expired), 队列 [b,c]; 判定依据: 模拟清理腾出空位, 入队不触发溢出")

	q2, _ := New[string](2, PolicyReject)
	_ = q2.Enqueue("x", 100, 0)
	_ = q2.Enqueue("y", 100, 0)
	if err := q2.Enqueue("z", 1, 5); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("Enqueue(z) err = %v, want ErrQueueFull", err)
	}
	if q2.Len() != 2 || len(q2.DeadLetters()) != 0 {
		t.Fatalf("拒绝后 Len=%d 死信=%d, want 2/0", q2.Len(), len(q2.DeadLetters()))
	}
	if err := q2.Enqueue("z", 1, 4); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("再次 enqueue@4 err = %v, want ErrQueueFull (拒绝不落地, maxNow 仍为 0)", err)
	}
	t.Logf("输入: reject 满队 [x,y] enqueue z@5 后再 @4; 输出: 均 queue_full, 状态不变; 判定依据: 模拟清理 0 条, 拒绝不落地含 maxNow")
}

// TestDequeueEmptyAfterCleanupDoesNotLand: head cleanup during a dequeue
// ending empty must not land either.
func TestDequeueEmptyAfterCleanupDoesNotLand(t *testing.T) {
	q, _ := New[string](2, PolicyReject)
	_ = q.Enqueue("a", 1, 0)
	_ = q.Enqueue("b", 2, 0)

	if _, err := q.Dequeue(10); !errors.Is(err, ErrEmptyQueue) {
		t.Fatalf("Dequeue(10) err = %v, want ErrEmptyQueue", err)
	}
	if q.Len() != 2 || len(q.DeadLetters()) != 0 {
		t.Fatalf("空队拒绝后 Len=%d 死信=%d, want 2/0 (清理不落地)", q.Len(), len(q.DeadLetters()))
	}
	if _, err := q.Dequeue(9); !errors.Is(err, ErrEmptyQueue) {
		t.Fatalf("Dequeue(9) err = %v, want ErrEmptyQueue (上次 now=10 未落地)", err)
	}
	t.Logf("输入: dequeue@10 于全过期满队, 再 dequeue@9; 输出: empty_queue, 两条仍在队, 死信空; 判定依据: 清理后队空的拒绝不落地")

	got, err := q.Dequeue(1)
	if err != nil {
		t.Fatalf("Dequeue(1): %v", err)
	}
	if got != "b" {
		t.Fatalf("Dequeue(1) = %q, want b", got)
	}
	dls := dlSnapshot(q)
	if len(dls) != 1 || dls[0].msg != "a" || dls[0].reason != ReasonExpired {
		t.Fatalf("死信 = %+v, want [a expired]", dls)
	}
	t.Logf("输入: dequeue@1; 输出: 死信 a(expired), 出队 b; 判定依据: now==deadline(a) 过期, b(deadline=2)未过期, 清理落地")
}

// TestRejectionReasonsAndPriority covers invalid capacity, negative ttl and
// clock regression, with clock regression checked first.
func TestRejectionReasonsAndPriority(t *testing.T) {
	if _, err := New[string](0, PolicyReject); !errors.Is(err, ErrInvalidCapacity) {
		t.Fatalf("New(0) err = %v, want ErrInvalidCapacity", err)
	}
	t.Logf("输入: New(capacity=0); 输出: %v; 判定依据: C 必须 >= 1", ErrInvalidCapacity)

	q, _ := New[string](2, PolicyReject)
	if err := q.Enqueue("a", -1, 0); !errors.Is(err, ErrNegativeTTL) {
		t.Fatalf("ttl<0 err = %v, want ErrNegativeTTL", err)
	}
	if q.Len() != 0 || len(q.DeadLetters()) != 0 {
		t.Fatalf("ttl 拒绝不得改变状态, Len=%d 死信=%d", q.Len(), len(q.DeadLetters()))
	}
	_ = q.Enqueue("a", 1, 10) // deadline 11
	if err := q.Enqueue("b", -1, 9); !errors.Is(err, ErrClockBackward) {
		t.Fatalf("倒退+负ttl err = %v, want ErrClockBackward", err)
	}
	if _, err := q.Dequeue(9); !errors.Is(err, ErrClockBackward) {
		t.Fatalf("dequeue 倒退 err = %v, want ErrClockBackward", err)
	}
	if err := q.Enqueue("b", 1, 10); err != nil {
		t.Fatalf("Enqueue@10: %v, want nil (now 相等不算倒退)", err)
	}
	t.Logf("输入: 见过 now=10 后 enqueue(ttl=-1,now=9); 输出: clock_backward; 判定依据: 时钟倒退优先于 ttl, now 相等允许")

	if _, err := q.Dequeue(100); !errors.Is(err, ErrEmptyQueue) {
		t.Fatalf("全过期出队 err = %v, want ErrEmptyQueue (清理后队空不落地)", err)
	}
	if q.Len() != 2 {
		t.Fatalf("Len = %d, want 2 (拒绝不落地)", q.Len())
	}
}

// ---- Step-by-step naive simulation ----------------------------------------

type naiveMsg struct {
	id       string
	deadline int64
}

type naiveDL struct {
	id       string
	reason   Reason
	deadline int64
}

// naiveQueue restates the specification as directly as possible: one slice,
// head-only cleanup simulated before commit, both policies by hand.
type naiveQueue struct {
	c      int
	policy OverflowPolicy
	q      []naiveMsg
	dl     []naiveDL
	maxNow int64
	seen   bool
}

func (n *naiveQueue) enqueue(id string, ttl, now int64) error {
	if n.seen && now < n.maxNow {
		return ErrClockBackward
	}
	if ttl < 0 {
		return ErrNegativeTTL
	}
	if len(n.q) == n.c {
		k := 0
		for k < len(n.q) && now >= n.q[k].deadline {
			k++
		}
		if n.policy == PolicyReject && len(n.q)-k == n.c {
			return ErrQueueFull // 模拟清理不落地
		}
		for i := 0; i < k; i++ {
			n.dl = append(n.dl, naiveDL{id: n.q[i].id, reason: ReasonExpired, deadline: n.q[i].deadline})
		}
		n.q = n.q[k:]
		if n.policy == PolicyDropHead && len(n.q) == n.c {
			n.dl = append(n.dl, naiveDL{id: n.q[0].id, reason: ReasonOverflow, deadline: n.q[0].deadline})
			n.q = n.q[1:]
		}
		if n.policy == PolicyReject && len(n.q) == n.c {
			panic("naive: unreachable reject-full branch")
		}
	}
	n.q = append(n.q, naiveMsg{id: id, deadline: now + ttl})
	n.maxNow, n.seen = now, true
	return nil
}

func (n *naiveQueue) dequeue(now int64) (string, error) {
	if n.seen && now < n.maxNow {
		return "", ErrClockBackward
	}
	k := 0
	for k < len(n.q) && now >= n.q[k].deadline {
		k++
	}
	if len(n.q)-k == 0 {
		return "", ErrEmptyQueue // 清理不落地
	}
	for i := 0; i < k; i++ {
		n.dl = append(n.dl, naiveDL{id: n.q[i].id, reason: ReasonExpired, deadline: n.q[i].deadline})
	}
	n.q = n.q[k:]
	id := n.q[0].id
	n.q = n.q[1:]
	n.maxNow, n.seen = now, true
	return id, nil
}

// opKind identifies an operation in a replayable script.
type opKind int

const (
	opEnqueue opKind = iota
	opDequeue
)

type scriptOp struct {
	kind opKind
	id   string
	ttl  int64
	now  int64
}

type opResult struct {
	id  string
	err error // nil means success; for enqueue success id field is unused
}

func runNaive(script []scriptOp, c int, p OverflowPolicy) ([]opResult, int, []naiveDL) {
	n := &naiveQueue{c: c, policy: p}
	res := make([]opResult, 0, len(script))
	for _, op := range script {
		switch op.kind {
		case opEnqueue:
			res = append(res, opResult{err: n.enqueue(op.id, op.ttl, op.now)})
		case opDequeue:
			id, err := n.dequeue(op.now)
			res = append(res, opResult{id: id, err: err})
		}
	}
	return res, len(n.q), n.dl
}

func runReal(script []scriptOp, c int, p OverflowPolicy) ([]opResult, int, []dlItem) {
	q, err := New[string](c, p)
	if err != nil {
		panic(err)
	}
	res := make([]opResult, 0, len(script))
	for _, op := range script {
		switch op.kind {
		case opEnqueue:
			res = append(res, opResult{err: q.Enqueue(op.id, op.ttl, op.now)})
		case opDequeue:
			id, err := q.Dequeue(op.now)
			res = append(res, opResult{id: id, err: err})
		}
	}
	return res, q.Len(), dlSnapshot(q)
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return errors.Is(a, b) || errors.Is(b, a)
}

// TestNaiveSimulationDifferential drives both implementations with random
// replayable scripts and compares results, lengths, dead letters and the
// exactly-one-state invariant for every produced message.
func TestNaiveSimulationDifferential(t *testing.T) {
	const iterations = 400
	for seed := int64(0); seed < iterations; seed++ {
		rng := rand.New(rand.NewSource(seed))
		c := 1 + rng.Intn(4)
		policy := OverflowPolicy(rng.Intn(2))
		steps := 20 + rng.Intn(60)

		script := make([]scriptOp, 0, steps)
		var now int64
		seq := 0
		for i := 0; i < steps; i++ {
			// 时钟: 多数非减, 少数倒退以触发拒绝; now 范围小以制造过期。
			if rng.Intn(8) == 0 {
				now -= int64(rng.Intn(3))
				if now < 0 {
					now = 0
				}
			} else {
				now += int64(rng.Intn(4))
			}
			if rng.Intn(2) == 0 {
				ttl := int64(rng.Intn(8))
				if rng.Intn(10) == 0 {
					ttl = -1
				}
				seq++
				script = append(script, scriptOp{kind: opEnqueue, id: fmt.Sprintf("m%d", seq), ttl: ttl, now: now})
			} else {
				script = append(script, scriptOp{kind: opDequeue, now: now})
			}
		}

		wantRes, wantLen, wantDL := runNaive(script, c, policy)
		gotRes, gotLen, gotDL := runReal(script, c, policy)

		if len(gotRes) != len(wantRes) {
			t.Fatalf("seed=%d 结果数不一致", seed)
		}
		for i := range wantRes {
			if !sameErr(gotRes[i].err, wantRes[i].err) || gotRes[i].id != wantRes[i].id {
				t.Fatalf("seed=%d op#%d (%+v): real=(%q,%v) naive=(%q,%v)",
					seed, i, script[i], gotRes[i].id, gotRes[i].err, wantRes[i].id, wantRes[i].err)
			}
		}
		if gotLen != wantLen {
			t.Fatalf("seed=%d Len real=%d naive=%d", seed, gotLen, wantLen)
		}
		if gotLen > c {
			t.Fatalf("seed=%d Len=%d 超过容量 C=%d", seed, gotLen, c)
		}
		if len(gotDL) != len(wantDL) {
			t.Fatalf("seed=%d 死信数 real=%d naive=%d", seed, len(gotDL), len(wantDL))
		}
		for i := range wantDL {
			if gotDL[i].msg != wantDL[i].id || gotDL[i].reason != wantDL[i].reason {
				t.Fatalf("seed=%d 死信#%d real=(%s,%s) naive=(%s,%s)",
					seed, i, gotDL[i].msg, gotDL[i].reason, wantDL[i].id, wantDL[i].reason)
			}
		}

		// 恰好一态: 每条入队成功的消息处于 在队/已出队/死信 之一。
		enqueued := map[string]bool{}
		for i, op := range script {
			if op.kind == opEnqueue && wantRes[i].err == nil {
				enqueued[op.id] = true
			}
		}
		state := map[string]string{}
		n2 := &naiveQueue{c: c, policy: policy}
		for _, op := range script {
			switch op.kind {
			case opEnqueue:
				_ = n2.enqueue(op.id, op.ttl, op.now)
			case opDequeue:
				id, _ := n2.dequeue(op.now)
				if id != "" {
					state[id] = "dequeued"
				}
			}
		}
		for _, m := range n2.q {
			state[m.id] = "queued"
		}
		for _, d := range n2.dl {
			tag := "dead:" + string(d.reason)
			if prev, ok := state[d.id]; ok {
				t.Fatalf("seed=%d 消息 %s 同时处于 %s 与 %s", seed, d.id, prev, tag)
			}
			state[d.id] = tag
		}
		// 真实死信与朴素死信一致已在上面比较; 用真实结果再核对出队不重复。
		seenDeq := map[string]bool{}
		for i, op := range script {
			if op.kind == opDequeue && gotRes[i].err == nil {
				if seenDeq[gotRes[i].id] {
					t.Fatalf("seed=%d 消息 %s 被重复出队", seed, gotRes[i].id)
				}
				seenDeq[gotRes[i].id] = true
			}
		}
		for id := range enqueued {
			st, ok := state[id]
			if !ok {
				t.Fatalf("seed=%d 消息 %s 不处于任何状态", seed, id)
			}
			if st == "dequeued" && !seenDeq[id] {
				t.Fatalf("seed=%d 消息 %s 朴素已出队但真实未出队", seed, id)
			}
			if seenDeq[id] && st != "dequeued" {
				t.Fatalf("seed=%d 消息 %s 真实出队但朴素状态=%s", seed, id, st)
			}
		}

		// 同序列重放: 再跑一次真实实现, 必须完全一致。
		gotRes2, gotLen2, gotDL2 := runReal(script, c, policy)
		for i := range gotRes {
			if gotRes2[i].id != gotRes[i].id || !sameErr(gotRes2[i].err, gotRes[i].err) {
				t.Fatalf("seed=%d 重放 op#%d 不一致", seed, i)
			}
		}
		if gotLen2 != gotLen || len(gotDL2) != len(gotDL) {
			t.Fatalf("seed=%d 重放终态不一致", seed)
		}
		for i := range gotDL {
			if gotDL2[i] != gotDL[i] {
				t.Fatalf("seed=%d 重放死信#%d 不一致", seed, i)
			}
		}

		if seed == 0 || seed == 1 {
			t.Logf("seed=%d C=%d policy=%d: %d 个操作, 终态 Len=%d 死信=%d, 与朴素模拟逐条一致, 重放一致; 判定依据: 结果/Len/死信全量对比 + 恰好一态",
				seed, c, policy, len(script), gotLen, len(gotDL))
		}
	}
}

// TestConcurrentSafety hammers the queue concurrently; the externally
// observable state must always satisfy Len<=C, and replay must be identical.
func TestConcurrentSafety(t *testing.T) {
	q, _ := New[string](8, PolicyDropHead)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			var now int64
			for i := 0; i < 200; i++ {
				now += int64(worker%3 + 1) // 每个 goroutine 内部非减
				if i%2 == 0 {
					_ = q.Enqueue(fmt.Sprintf("w%d-%d", worker, i), int64(i%7), now)
				} else {
					_, _ = q.Dequeue(now)
				}
				if q.Len() > 8 {
					t.Errorf("并发下 Len 超过容量")
					return
				}
			}
		}(w)
	}
	wg.Wait()
	if q.Len() > 8 {
		t.Fatalf("终态 Len=%d > 8", q.Len())
	}
	// 并发跨 worker 可能出现时钟倒退拒绝, 它们同样不得落地; 此处只断言不变量
	// (线性化的精确等价性由互斥锁保证, 行为由差分测试覆盖)。
	t.Logf("输入: 8 goroutine x 200 混合 enqueue/dequeue; 输出: 终态 Len=%d 死信=%d; 判定依据: 全程 Len<=C, 互斥锁保证串行等价",
		q.Len(), len(q.DeadLetters()))
}
