package dlq

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// ---------- 测试辅助 ----------

func mustEnqueue(t *testing.T, s *System, queue, body string) int64 {
	t.Helper()
	oseq, err := s.Enqueue(queue, body)
	if err != nil {
		t.Fatalf("Enqueue(%q,%q) 意外失败: %v", queue, body, err)
	}
	t.Logf("输入 Enqueue(%q,%q) -> 输出 oseq=%d", queue, body, oseq)
	return oseq
}

func mustDead(t *testing.T, s *System, queue string) int64 {
	t.Helper()
	dseq, err := s.SendToDead(queue)
	if err != nil {
		t.Fatalf("SendToDead(%q) 意外失败: %v", queue, err)
	}
	t.Logf("输入 SendToDead(%q) -> 输出 dseq=%d", queue, dseq)
	return dseq
}

func mustReplay(t *testing.T, s *System, queue string, n int) int {
	t.Helper()
	got, err := s.Replay(queue, n)
	if err != nil {
		t.Fatalf("Replay(%q,%d) 意外失败: %v", queue, n, err)
	}
	t.Logf("输入 Replay(%q,%d) -> 输出 placed=%d", queue, n, got)
	return got
}

func rejectReason(err error) RejectReason {
	var re *RejectError
	if !errors.Is(err, ErrRejected) || !errors.As(err, &re) {
		return ""
	}
	return re.Reason
}

func mustQueue(t *testing.T, s *System, queue string) []Message {
	t.Helper()
	q, err := s.QueueSnapshot(queue)
	if err != nil {
		t.Fatalf("QueueSnapshot(%q) 意外失败: %v", queue, err)
	}
	return q
}

func oseqs(ms []Message) []int64 {
	out := make([]int64, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.OSeq)
	}
	return out
}

func deadOSeqs(es []DeadEntry) []int64 {
	out := make([]int64, 0, len(es))
	for _, e := range es {
		out = append(out, e.OSeq)
	}
	return out
}

// snapshot 返回 (队列内容, 死信区内容, 死信区归属) 用于拒绝前后比对。
func snapshot(t *testing.T, s *System, queues ...string) string {
	t.Helper()
	state := ""
	for _, q := range queues {
		state += fmt.Sprintf("%q=%+v ", q, mustQueue(t, s, q))
	}
	state += fmt.Sprintf("dead=%+v queues=%v", s.DeadSnapshot(), s.DeadQueues())
	return state
}

// ---------- 边界用例 ----------

// 重放按 oseq 升序而非死亡序：让 oseq 较小的消息较晚进入死信区。
func TestReplayOrderByOSeqNotDSeq(t *testing.T) {
	s := New(10, 3)
	s.AddQueue("q")

	o1 := mustEnqueue(t, s, "q", "m1")
	o2 := mustEnqueue(t, s, "q", "m2")
	mustDead(t, s, "q")      // m1 先死 (dseq=1)
	mustReplay(t, s, "q", 1) // m1 放回队尾，队列: [m2, m1]
	mustDead(t, s, "q")      // m2 死 (dseq=2)
	mustDead(t, s, "q")      // m1 再次死 (dseq=3)，死信区死亡序: m2(d2), m1(d3)

	got := mustReplay(t, s, "q", 2)
	if got != 2 {
		t.Fatalf("应放回 2 条, 实际 %d", got)
	}
	q := mustQueue(t, s, "q")
	t.Logf("判定依据: 死信区死亡序为 [oseq=%d(d2), oseq=%d(d3)]，重放应按 oseq 升序先放 oseq=%d", o2, o1, o1)
	if want := []int64{o1, o2}; !reflect.DeepEqual(oseqs(q), want) {
		t.Fatalf("重放顺序错误: 队列 oseq=%v, 期望 %v（按 oseq 升序而非死亡序）", oseqs(q), want)
	}
	if d := s.DeadSnapshot(); len(d) != 0 {
		t.Fatalf("死信区应为空, 实际 %+v", d)
	}
}

// 重放次数恰等于 R 的条目被跳过且不占名额；跳过后继续取后面的条目。
func TestReplaySkipsExhaustedWithoutConsumingQuota(t *testing.T) {
	s := New(10, 1) // R = 1
	s.AddQueue("q")

	o1 := mustEnqueue(t, s, "q", "m1")
	o2 := mustEnqueue(t, s, "q", "m2")
	mustDead(t, s, "q")      // m1 死
	mustReplay(t, s, "q", 1) // m1 放回，replays=1 恰等于 R
	mustDead(t, s, "q")      // m2 死
	mustDead(t, s, "q")      // m1 再死；死信区: m2(replays=0), m1(replays=1=R)

	got := mustReplay(t, s, "q", 2) // 名额 2，但 m1 已达上限被跳过且不占名额
	t.Logf("判定依据: m1(oseq=%d) 重放次数恰等于 R=1 应被跳过且不占名额, m2(oseq=%d) 仍应被放回", o1, o2)
	if got != 1 {
		t.Fatalf("被跳过条目不应占用名额: 期望放回 1 条, 实际 %d", got)
	}
	if q := mustQueue(t, s, "q"); !reflect.DeepEqual(oseqs(q), []int64{o2}) {
		t.Fatalf("队列应只含 m2, 实际 oseq=%v", oseqs(q))
	}
	d := s.DeadSnapshot()
	if len(d) != 1 || d[0].OSeq != o1 || d[0].Replays != 1 {
		t.Fatalf("已达上限的 m1 应留在死信区且次数不变, 实际 %+v", d)
	}
}

// 容量中途耗尽：部分重放，已放回的不回滚，其余留在死信区。
func TestReplayPartialOnCapacityExhaustion(t *testing.T) {
	s := New(3, 5) // C = 3
	s.AddQueue("q")

	mustEnqueue(t, s, "q", "a")
	mustEnqueue(t, s, "q", "b")
	mustEnqueue(t, s, "q", "c")
	mustDead(t, s, "q") // a 死
	mustDead(t, s, "q") // b 死
	mustDead(t, s, "q") // c 死；队列空，死信区 3 条
	mustEnqueue(t, s, "q", "x")
	mustEnqueue(t, s, "q", "y") // 队列剩 1 个空位

	got := mustReplay(t, s, "q", 3)
	t.Logf("判定依据: 容量 C=3, 已有 2 条, 仅 1 个空位, 应只放回 1 条且不回滚")
	if got != 1 {
		t.Fatalf("容量中途耗尽可能部分重放: 期望放回 1 条, 实际 %d", got)
	}
	q := mustQueue(t, s, "q")
	if len(q) != 3 || q[2].Body != "a" {
		t.Fatalf("队列应含 x,y,a, 实际 %+v", q)
	}
	d := s.DeadSnapshot()
	if len(d) != 2 || d[0].Body != "b" || d[1].Body != "c" {
		t.Fatalf("其余条目应留在死信区, 实际 %+v", d)
	}
	if d[0].Replays != 0 || d[1].Replays != 0 {
		t.Fatalf("未放回条目的重放次数不应变化, 实际 %+v", d)
	}
}

// 零进展时报“目标队列已满”；有进展时不报错。
func TestReplayQueueFullOnlyWhenZeroProgress(t *testing.T) {
	s := New(2, 5) // C = 2
	s.AddQueue("q")

	mustEnqueue(t, s, "q", "a")
	mustEnqueue(t, s, "q", "b")
	mustDead(t, s, "q")         // a 死
	mustEnqueue(t, s, "q", "c") // 队列重新满: [b, c]

	before := snapshot(t, s, "q")
	_, err := s.Replay("q", 1)
	t.Logf("输入 Replay(q,1) 目标队列已满 -> 输出 err=%v；判定依据: 零进展必须报 queue_full", err)
	if rejectReason(err) != ReasonQueueFull {
		t.Fatalf("零进展应报 %q, 实际 %v", ReasonQueueFull, err)
	}
	if after := snapshot(t, s, "q"); after != before {
		t.Fatalf("被拒绝的操作不得改变状态\n前: %s\n后: %s", before, after)
	}

	if _, err := s.Dequeue("q"); err != nil { // 腾出一个空位
		t.Fatalf("Dequeue 意外失败: %v", err)
	}
	got, err := s.Replay("q", 1)
	t.Logf("输入 Replay(q,1) 有 1 个空位 -> 输出 placed=%d err=%v；判定依据: 有进展不属于拒绝", got, err)
	if err != nil || got != 1 {
		t.Fatalf("有进展不应报错: placed=%d err=%v", got, err)
	}
}

// 再次进入死信区：分配新死亡序号，oseq 与累计重放次数保持不变。
func TestReDeathKeepsOSeqAndReplayCount(t *testing.T) {
	s := New(10, 5)
	s.AddQueue("q")

	o1 := mustEnqueue(t, s, "q", "m1")
	mustEnqueue(t, s, "q", "m2")
	d1 := mustDead(t, s, "q") // m1 死 (dseq=1)
	mustReplay(t, s, "q", 1)  // m1 放回, replays=1
	d2 := mustDead(t, s, "q") // m2 死 (dseq=2)
	d3 := mustDead(t, s, "q") // m1 再次死 (dseq=3)

	t.Logf("判定依据: m1 再次死信应分配新 dseq(%d != %d), oseq 保持 %d, 累计重放次数保持 1", d3, d1, o1)
	d := s.DeadSnapshot()
	if len(d) != 2 {
		t.Fatalf("死信区应有 2 条, 实际 %+v", d)
	}
	m1 := d[1]
	if m1.OSeq != o1 || m1.DSeq != d3 || m1.Replays != 1 {
		t.Fatalf("再次死信后 oseq/累计次数应保持: 期望 oseq=%d dseq=%d replays=1, 实际 %+v", o1, d3, m1)
	}
	if d3 <= d2 || d3 <= d1 {
		t.Fatalf("新死亡序号应递增: d1=%d d2=%d d3=%d", d1, d2, d3)
	}
}

// 拒绝原因优先级与“被拒绝不改变状态”。
func TestRejectReasonsAndAtomicity(t *testing.T) {
	s := New(2, 1)
	s.AddQueue("q")
	mustEnqueue(t, s, "q", "a")
	mustDead(t, s, "q")      // a 死
	mustReplay(t, s, "q", 1) // a 放回, replays=1=R
	mustDead(t, s, "q")      // a 再死，且已达上限

	cases := []struct {
		name   string
		queue  string
		n      int
		reason RejectReason
	}{
		{"n小于1", "q", 0, ReasonInvalidArgument},
		{"队列名为空", "", 1, ReasonInvalidArgument},
		{"队列不存在", "ghost", 1, ReasonQueueNotFound},
		{"死信区无该队列条目", "q", 1, ReasonAllExhausted}, // 有条目但全部达上限
	}
	for _, tc := range cases {
		before := snapshot(t, s, "q")
		_, err := s.Replay(tc.queue, tc.n)
		t.Logf("输入 Replay(%q,%d) -> 输出 err=%v；判定依据: 应拒绝为 %s", tc.queue, tc.n, err, tc.reason)
		if rejectReason(err) != tc.reason {
			t.Errorf("%s: 期望拒绝原因 %q, 实际 %v", tc.name, tc.reason, err)
		}
		if after := snapshot(t, s, "q"); after != before {
			t.Errorf("%s: 被拒绝的操作改变了状态\n前: %s\n后: %s", tc.name, before, after)
		}
	}

	// 死信区完全没有该队列条目 -> no_dead_entries
	s2 := New(2, 1)
	s2.AddQueue("q")
	s2.AddQueue("other")
	mustEnqueue(t, s2, "other", "x")
	mustDead(t, s2, "other")
	_, err := s2.Replay("q", 1)
	t.Logf("输入 Replay(q,1) 死信区只有 other 的条目 -> 输出 err=%v；判定依据: 应拒绝为 no_dead_entries", err)
	if rejectReason(err) != ReasonNoDeadEntries {
		t.Fatalf("期望 %q, 实际 %v", ReasonNoDeadEntries, err)
	}
}

// ---------- 朴素逐步模拟 ----------

// simMsg / simDead / simulator 是按规则逐条写成的朴素参考实现，
// 用于与 System 在随机操作序列下逐步对照。
type simMsg struct {
	oseq    int64
	body    string
	replays int
}

type simDead struct {
	msg   simMsg
	queue string
	dseq  int64
}

type simulator struct {
	cap, limit   int
	nextO, nextD int64
	queues       map[string][]simMsg
	dead         []simDead
}

func newSimulator(cap, limit int, names ...string) *simulator {
	sim := &simulator{cap: cap, limit: limit, nextO: 1, nextD: 1, queues: map[string][]simMsg{}}
	for _, n := range names {
		sim.queues[n] = nil
	}
	return sim
}

func (sim *simulator) enqueue(queue, body string) (int64, RejectReason) {
	q, ok := sim.queues[queue]
	if !ok {
		return 0, ReasonQueueNotFound
	}
	if len(q) >= sim.cap {
		return 0, ReasonQueueFull
	}
	oseq := sim.nextO
	sim.nextO++
	sim.queues[queue] = append(q, simMsg{oseq: oseq, body: body})
	return oseq, ""
}

func (sim *simulator) dequeue(queue string) RejectReason {
	q, ok := sim.queues[queue]
	if !ok {
		return ReasonQueueNotFound
	}
	if len(q) == 0 {
		return ReasonNoDeadEntries
	}
	sim.queues[queue] = q[1:]
	return ""
}

func (sim *simulator) sendToDead(queue string) (int64, RejectReason) {
	q, ok := sim.queues[queue]
	if !ok {
		return 0, ReasonQueueNotFound
	}
	if len(q) == 0 {
		return 0, ReasonNoDeadEntries
	}
	m := q[0]
	sim.queues[queue] = q[1:]
	dseq := sim.nextD
	sim.nextD++
	sim.dead = append(sim.dead, simDead{msg: m, queue: queue, dseq: dseq})
	return dseq, ""
}

func (sim *simulator) replay(queue string, n int) (int, RejectReason) {
	if n < 1 || queue == "" {
		return 0, ReasonInvalidArgument
	}
	q, ok := sim.queues[queue]
	if !ok {
		return 0, ReasonQueueNotFound
	}
	var mine []int
	for i, e := range sim.dead {
		if e.queue == queue {
			mine = append(mine, i)
		}
	}
	if len(mine) == 0 {
		return 0, ReasonNoDeadEntries
	}
	var eligible []int
	for _, i := range mine {
		if sim.dead[i].msg.replays < sim.limit {
			eligible = append(eligible, i)
		}
	}
	if len(eligible) == 0 {
		return 0, ReasonAllExhausted
	}
	// 按 oseq 升序选择（朴素：反复取最小值）。
	placed := 0
	used := map[int]bool{}
	for placed < n && len(q) < sim.cap {
		best := -1
		for _, i := range eligible {
			if used[i] {
				continue
			}
			if best == -1 || sim.dead[i].msg.oseq < sim.dead[best].msg.oseq {
				best = i
			}
		}
		if best == -1 {
			break
		}
		used[best] = true
		sim.dead[best].msg.replays++
		q = append(q, sim.dead[best].msg)
		placed++
	}
	if placed == 0 {
		return 0, ReasonQueueFull
	}
	sim.queues[queue] = q
	var kept []simDead
	for i, e := range sim.dead {
		if !used[i] {
			kept = append(kept, e)
		}
	}
	sim.dead = kept
	return placed, ""
}

// stateString 渲染系统状态，用于逐步对照与日志。
func stateString(t *testing.T, s *System, queues []string) string {
	t.Helper()
	out := ""
	for _, q := range queues {
		snap, err := s.QueueSnapshot(q)
		if err != nil {
			t.Fatalf("QueueSnapshot(%q): %v", q, err)
		}
		out += fmt.Sprintf("%s=%v ", q, oseqs(snap))
	}
	out += fmt.Sprintf("dead=%v deadQueues=%v", deadOSeqs(s.DeadSnapshot()), s.DeadQueues())
	return out
}

func simStateString(sim *simulator, queues []string) string {
	out := ""
	for _, q := range queues {
		var ids []int64
		for _, m := range sim.queues[q] {
			ids = append(ids, m.oseq)
		}
		out += fmt.Sprintf("%s=%v ", q, ids)
	}
	var ids []int64
	var qs []string
	for _, e := range sim.dead {
		ids = append(ids, e.msg.oseq)
		qs = append(qs, e.queue)
	}
	return out + fmt.Sprintf("dead=%v deadQueues=%v", ids, qs)
}

// 随机操作序列下，System 与朴素模拟逐步对照（结果、拒绝原因、状态全比对）。
func TestAgainstNaiveSimulation(t *testing.T) {
	const steps = 3000
	queues := []string{"q1", "q2", "q3"}
	s := New(3, 2) // C=3, R=2
	for _, q := range queues {
		s.AddQueue(q)
	}
	sim := newSimulator(3, 2, queues...)
	rng := rand.New(rand.NewSource(20261001))

	for step := 0; step < steps; step++ {
		q := queues[rng.Intn(len(queues))]
		var gotState, wantState, desc string

		switch rng.Intn(5) {
		case 0: // 入队
			body := fmt.Sprintf("m%d", step)
			oseq, err := s.Enqueue(q, body)
			wantOseq, wantReason := sim.enqueue(q, body)
			if rejectReason(err) != wantReason || (err == nil && oseq != wantOseq) {
				t.Fatalf("步骤 %d Enqueue(%q,%q): got(oseq=%d,reason=%q) want(oseq=%d,reason=%q)",
					step, q, body, oseq, rejectReason(err), wantOseq, wantReason)
			}
			desc = fmt.Sprintf("Enqueue(%q,%q) -> oseq=%d reason=%q", q, body, oseq, rejectReason(err))
		case 1: // 出队
			_, err := s.Dequeue(q)
			wantReason := sim.dequeue(q)
			if rejectReason(err) != wantReason {
				t.Fatalf("步骤 %d Dequeue(%q): got reason=%q want %q", step, q, rejectReason(err), wantReason)
			}
			desc = fmt.Sprintf("Dequeue(%q) -> reason=%q", q, rejectReason(err))
		case 2: // 送入死信
			dseq, err := s.SendToDead(q)
			wantDseq, wantReason := sim.sendToDead(q)
			if rejectReason(err) != wantReason || (err == nil && dseq != wantDseq) {
				t.Fatalf("步骤 %d SendToDead(%q): got(dseq=%d,reason=%q) want(dseq=%d,reason=%q)",
					step, q, dseq, rejectReason(err), wantDseq, wantReason)
			}
			desc = fmt.Sprintf("SendToDead(%q) -> dseq=%d reason=%q", q, dseq, rejectReason(err))
		case 3: // 重放
			n := rng.Intn(5) // 含 0，覆盖非法参数
			placed, err := s.Replay(q, n)
			wantPlaced, wantReason := sim.replay(q, n)
			if rejectReason(err) != wantReason || placed != wantPlaced {
				t.Fatalf("步骤 %d Replay(%q,%d): got(placed=%d,reason=%q) want(placed=%d,reason=%q)",
					step, q, n, placed, rejectReason(err), wantPlaced, wantReason)
			}
			desc = fmt.Sprintf("Replay(%q,%d) -> placed=%d reason=%q", q, n, placed, rejectReason(err))
		case 4: // 查询（只验证不改变状态）
			before := stateString(t, s, queues)
			_ = s.DeadSnapshot()
			_, _ = s.QueueSnapshot(q)
			if after := stateString(t, s, queues); after != before {
				t.Fatalf("步骤 %d 查询改变了状态", step)
			}
			desc = fmt.Sprintf("Query(%q) -> 状态不变", q)
		}

		gotState = stateString(t, s, queues)
		wantState = simStateString(sim, queues)
		if gotState != wantState {
			t.Fatalf("步骤 %d 状态不一致\n操作: %s\nSystem: %s\n朴素模拟: %s", step, desc, gotState, wantState)
		}
		if step%500 == 0 {
			t.Logf("步骤 %d 输入/输出: %s | 判定依据: 与朴素模拟状态一致 %s", step, desc, gotState)
		}
	}
	t.Logf("共 %d 步随机操作全部与朴素模拟一致；最终状态: %s", steps, stateString(t, s, queues))
}

// 并发调用：结果等价于某个串行顺序，且每条消息恰好处于
// “队列中、死信区”之一或已被出队。
func TestConcurrentAccess(t *testing.T) {
	const workers = 8
	const opsPerWorker = 500
	queues := []string{"qa", "qb"}
	s := New(4, 2)
	for _, q := range queues {
		s.AddQueue(q)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	enqueued, dequeued := 0, 0
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id)))
			for i := 0; i < opsPerWorker; i++ {
				q := queues[rng.Intn(len(queues))]
				switch rng.Intn(5) {
				case 0:
					if _, err := s.Enqueue(q, "x"); err == nil {
						mu.Lock()
						enqueued++
						mu.Unlock()
					}
				case 1:
					if _, err := s.Dequeue(q); err == nil {
						mu.Lock()
						dequeued++
						mu.Unlock()
					}
				case 2:
					_, _ = s.SendToDead(q)
				case 3:
					_, _ = s.Replay(q, 1+rng.Intn(3))
				case 4:
					_ = s.DeadSnapshot()
					_, _ = s.QueueSnapshot(q)
				}
			}
		}(w)
	}
	wg.Wait()

	inQueues := 0
	for _, q := range queues {
		inQueues += len(mustQueue(t, s, q))
	}
	inDead := len(s.DeadSnapshot())
	t.Logf("判定依据: 入队成功 %d = 出队 %d + 队列中 %d + 死信区 %d（每条消息恰居其一）",
		enqueued, dequeued, inQueues, inDead)
	if enqueued != dequeued+inQueues+inDead {
		t.Fatalf("消息守恒被破坏: enqueued=%d dequeued=%d inQueues=%d inDead=%d",
			enqueued, dequeued, inQueues, inDead)
	}
}
