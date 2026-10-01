package delivery

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func enqueueN(t *testing.T, l *Ledger, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		seq := l.Enqueue(i)
		t.Logf("in: Enqueue(payload=%d) -> out: seq=%d (依据: 入队序号从1起只增不复用)", i, seq)
	}
}

func mustDeliver(t *testing.T, l *Ledger) Delivery {
	t.Helper()
	d, err := l.Deliver()
	if err != nil {
		t.Fatalf("Deliver failed: %v", err)
	}
	t.Logf("in: Deliver() -> out: tag=%d seq=%d redelivered=%v (依据: 取入队序号最小者, 标签递增)", d.Tag, d.Seq, d.Redelivered)
	return d
}

func wantErr(t *testing.T, op string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: got error %v, want %v", op, err, want)
	}
	t.Logf("in: %s -> out: err=%v (依据: 拒绝原因可区分且操作不改变状态)", op, err)
}

func wantQueueSeqs(t *testing.T, l *Ledger, want []uint64) {
	t.Helper()
	got := l.Snapshot().QueueSeqs
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("queue seqs = %v, want %v", got, want)
	}
	t.Logf("out: queue seqs=%v (依据: 队列按入队序号升序持有消息)", got)
}

func TestBatchAckIncludesBoundaryTag(t *testing.T) {
	l := New(5)
	enqueueN(t, l, 5)
	for i := 0; i < 5; i++ {
		mustDeliver(t, l)
	}
	n, err := l.Ack(3, true)
	if err != nil || n != 3 {
		t.Fatalf("Ack(3, multiple) = %d, %v; want 3, nil", n, err)
	}
	t.Logf("in: Ack(tag=3, multiple=true) -> out: settled=%d (依据: 批量范围取标签<=3, 边界标签3含在内, 标签4排除)", n)
	snap := l.Snapshot()
	if !reflect.DeepEqual(snap.UnackedSeqs, map[uint64]uint64{4: 4, 5: 5}) {
		t.Fatalf("unacked = %v, want tags 4,5", snap.UnackedSeqs)
	}
	n, err = l.Ack(5, true)
	if err != nil || n != 2 {
		t.Fatalf("Ack(5, multiple) = %d, %v; want 2, nil", n, err)
	}
	t.Logf("in: Ack(tag=5, multiple=true) -> out: settled=%d (依据: 剩余未确认标签4,5均不大于5)", n)
	if got := l.Stats().Unacked; got != 0 {
		t.Fatalf("unacked = %d, want 0", got)
	}
}

func TestBatchRejectRequeueRepositionsBySeq(t *testing.T) {
	l := New(4)
	enqueueN(t, l, 5)
	for i := 0; i < 4; i++ {
		mustDeliver(t, l)
	}
	wantQueueSeqs(t, l, []uint64{5})

	if _, err := l.Reject(2, false, true); err != nil {
		t.Fatalf("Reject(2, single, requeue) failed: %v", err)
	}
	t.Logf("in: Reject(tag=2, multiple=false, requeue=true) -> out: seq=2 回队 (依据: 插在入队序号更大的5之前)")
	wantQueueSeqs(t, l, []uint64{2, 5})

	if _, err := l.Reject(4, false, true); err != nil {
		t.Fatalf("Reject(4, single, requeue) failed: %v", err)
	}
	t.Logf("in: Reject(tag=4, multiple=false, requeue=true) -> out: seq=4 回队 (依据: 归位到2与5之间, 非队首非队尾)")
	wantQueueSeqs(t, l, []uint64{2, 4, 5})

	d := mustDeliver(t, l)
	if d.Seq != 2 || !d.Redelivered {
		t.Fatalf("deliver = seq %d redelivered %v, want seq 2 redelivered", d.Seq, d.Redelivered)
	}
	d = mustDeliver(t, l)
	if d.Seq != 4 || !d.Redelivered {
		t.Fatalf("deliver = seq %d redelivered %v, want seq 4 redelivered", d.Seq, d.Redelivered)
	}
	if _, err := l.Ack(1, false); err != nil {
		t.Fatalf("Ack(1, single) failed: %v", err)
	}
	t.Logf("in: Ack(tag=1, multiple=false) -> out: 释放一个预取额度 (依据: 未确认数小于P时才允许投递)")
	d = mustDeliver(t, l)
	if d.Seq != 5 || d.Redelivered {
		t.Fatalf("deliver = seq %d redelivered %v, want seq 5 first delivery", d.Seq, d.Redelivered)
	}
}

func TestBatchRejectRequeueRestoresFullOrder(t *testing.T) {
	l := New(3)
	enqueueN(t, l, 5)
	for i := 0; i < 3; i++ {
		mustDeliver(t, l)
	}
	n, err := l.Reject(3, true, true)
	if err != nil || n != 3 {
		t.Fatalf("Reject(3, multiple, requeue) = %d, %v; want 3, nil", n, err)
	}
	t.Logf("in: Reject(tag=3, multiple=true, requeue=true) -> out: %d 条回队 (依据: 批量按标签取范围, 回队按入队序号升序归位)", n)
	wantQueueSeqs(t, l, []uint64{1, 2, 3, 4, 5})
}

func TestRequeuedTagLargerBatchAckRangesByTag(t *testing.T) {
	l := New(3)
	enqueueN(t, l, 3)
	d1 := mustDeliver(t, l)
	d2 := mustDeliver(t, l)
	d3 := mustDeliver(t, l)

	if _, err := l.Reject(d1.Tag, false, true); err != nil {
		t.Fatalf("Reject(%d, single, requeue) failed: %v", d1.Tag, err)
	}
	d4 := mustDeliver(t, l)
	if d4.Seq != d1.Seq || !d4.Redelivered {
		t.Fatalf("redeliver = seq %d redelivered %v, want seq %d redelivered", d4.Seq, d4.Redelivered, d1.Seq)
	}
	if d4.Tag <= d2.Tag || d4.Tag <= d3.Tag {
		t.Fatalf("requeued tag %d, want greater than %d and %d", d4.Tag, d2.Tag, d3.Tag)
	}
	t.Logf("out: 回队消息重投标签=%d 大于未回队者标签=%d,%d (依据: 重投分配新标签)", d4.Tag, d2.Tag, d3.Tag)

	n, err := l.Ack(d3.Tag, true)
	if err != nil || n != 2 {
		t.Fatalf("Ack(%d, multiple) = %d, %v; want 2, nil", d3.Tag, n, err)
	}
	t.Logf("in: Ack(tag=%d, multiple=true) -> out: settled=%d (依据: 批量按标签而非入队序号取范围, seq最小的消息标签为%d故不在范围内)", d3.Tag, n, d4.Tag)
	snap := l.Snapshot()
	if !reflect.DeepEqual(snap.UnackedSeqs, map[uint64]uint64{d4.Tag: d1.Seq}) {
		t.Fatalf("unacked = %v, want only tag %d", snap.UnackedSeqs, d4.Tag)
	}
	if _, err := l.Ack(d4.Tag, false); err != nil {
		t.Fatalf("Ack(%d, single) failed: %v", d4.Tag, err)
	}
	if got := l.Stats().Unacked; got != 0 {
		t.Fatalf("unacked = %d, want 0", got)
	}
}

func TestRedeliveredFlag(t *testing.T) {
	l := New(1)
	enqueueN(t, l, 1)
	d := mustDeliver(t, l)
	if d.Redelivered {
		t.Fatal("first delivery must have redelivered=false")
	}
	t.Logf("out: 首次投递 redelivered=false (依据: 重投标志首次为假)")
	if _, err := l.Reject(d.Tag, false, true); err != nil {
		t.Fatalf("Reject requeue failed: %v", err)
	}
	d2 := mustDeliver(t, l)
	if !d2.Redelivered || d2.Tag == d.Tag {
		t.Fatalf("redelivery = tag %d redelivered %v, want new tag and redelivered=true", d2.Tag, d2.Redelivered)
	}
	t.Logf("out: 回队后再投递 tag=%d redelivered=true (依据: 回队后再次投递为真且分配新标签)", d2.Tag)
}

func TestPrefetchExactFitAndFull(t *testing.T) {
	l := New(2)
	enqueueN(t, l, 3)
	mustDeliver(t, l)
	mustDeliver(t, l)
	if got := l.Stats().Unacked; got != 2 {
		t.Fatalf("unacked = %d, want exactly prefetch 2", got)
	}
	t.Logf("out: 未确认数=2 恰好放满预取上限P=2 (依据: 未确认数不超过P)")
	if _, err := l.Deliver(); !errors.Is(err, ErrPrefetchFull) {
		t.Fatalf("Deliver at full prefetch: got %v, want ErrPrefetchFull", err)
	}
	t.Logf("in: Deliver() -> out: err=%v (依据: 未确认数达到P时拒绝投递)", ErrPrefetchFull)

	if _, err := l.Ack(1, false); err != nil {
		t.Fatalf("Ack failed: %v", err)
	}
	mustDeliver(t, l)
	if got := l.Stats().Unacked; got != 2 {
		t.Fatalf("unacked = %d, want exactly prefetch 2 after refill", got)
	}
	t.Logf("out: 确认一条后再投递, 未确认数回到2 恰好放下 (依据: 未确认数小于P时允许投递)")

	single := New(1)
	enqueueN(t, single, 1)
	mustDeliver(t, single)
	if got := single.Stats().Unacked; got != 1 {
		t.Fatalf("unacked = %d, want 1", got)
	}
	t.Logf("out: P=1 时投递一条后未确认数=1 恰好放满 (依据: 未确认数不超过P)")
}

func stateOf(l *Ledger) (Stats, Snapshot) {
	return l.Stats(), l.Snapshot()
}

func wantStateUnchanged(t *testing.T, l *Ledger, before Stats, beforeSnap Snapshot) {
	t.Helper()
	after, afterSnap := stateOf(l)
	if after != before || !reflect.DeepEqual(afterSnap, beforeSnap) {
		t.Fatalf("rejected op mutated state: stats %+v -> %+v, snapshot %+v -> %+v",
			before, after, beforeSnap, afterSnap)
	}
	t.Logf("out: 状态未变 stats=%+v (依据: 被拒绝的操作不得改变队列、标签计数、未确认集合与丢弃数)", after)
}

func TestRejectReasonInvalidTag(t *testing.T) {
	l := New(2)
	before, beforeSnap := stateOf(l)
	_, err := l.Ack(0, false)
	wantErr(t, "Ack(tag=0)", err, ErrInvalidTag)
	_, err = l.Reject(0, true, true)
	wantErr(t, "Reject(tag=0)", err, ErrInvalidTag)
	wantStateUnchanged(t, l, before, beforeSnap)

	enqueueN(t, l, 1)
	d := mustDeliver(t, l)
	before, beforeSnap = stateOf(l)
	_, err = l.Ack(d.Tag+1, false)
	wantErr(t, "Ack(tag=maxTag+1)", err, ErrInvalidTag)
	_, err = l.Reject(d.Tag+1, true, false)
	wantErr(t, "Reject(tag=maxTag+1)", err, ErrInvalidTag)
	wantStateUnchanged(t, l, before, beforeSnap)
}

func TestRejectReasonAlreadySettled(t *testing.T) {
	l := New(3)
	enqueueN(t, l, 3)
	d1 := mustDeliver(t, l)
	d2 := mustDeliver(t, l)
	d3 := mustDeliver(t, l)

	if _, err := l.Ack(d1.Tag, false); err != nil {
		t.Fatalf("Ack failed: %v", err)
	}
	if _, err := l.Reject(d2.Tag, false, false); err != nil {
		t.Fatalf("Reject drop failed: %v", err)
	}
	if _, err := l.Reject(d3.Tag, false, true); err != nil {
		t.Fatalf("Reject requeue failed: %v", err)
	}
	t.Logf("setup: tag=%d 已确认, tag=%d 已丢弃, tag=%d 已回队 (依据: 三种方式均为已结算)", d1.Tag, d2.Tag, d3.Tag)

	before, beforeSnap := stateOf(l)
	_, err := l.Ack(d1.Tag, false)
	wantErr(t, "Ack(已确认标签)", err, ErrAlreadySettled)
	_, err = l.Reject(d2.Tag, false, true)
	wantErr(t, "Reject(已丢弃标签)", err, ErrAlreadySettled)
	_, err = l.Ack(d3.Tag, false)
	wantErr(t, "Ack(已回队标签)", err, ErrAlreadySettled)
	wantStateUnchanged(t, l, before, beforeSnap)
}

func TestRejectReasonEmptyRange(t *testing.T) {
	l := New(2)
	enqueueN(t, l, 2)
	d1 := mustDeliver(t, l)
	d2 := mustDeliver(t, l)
	if _, err := l.Ack(d2.Tag, true); err != nil {
		t.Fatalf("Ack failed: %v", err)
	}
	t.Logf("setup: 标签 %d,%d 均已确认, 当前无未确认投递 (依据: 标签合法但范围内没有任何未确认投递)", d1.Tag, d2.Tag)

	before, beforeSnap := stateOf(l)
	_, err := l.Ack(d2.Tag, true)
	wantErr(t, "Ack(合法标签, multiple=true)", err, ErrEmptyRange)
	_, err = l.Reject(d1.Tag, true, true)
	wantErr(t, "Reject(合法标签, multiple=true)", err, ErrEmptyRange)
	wantStateUnchanged(t, l, before, beforeSnap)
}

func TestDeliverRejectReasonsAndPriority(t *testing.T) {
	l := New(1)
	before, beforeSnap := stateOf(l)
	_, err := l.Deliver()
	wantErr(t, "Deliver(空队列)", err, ErrQueueEmpty)
	wantStateUnchanged(t, l, before, beforeSnap)

	enqueueN(t, l, 2)
	mustDeliver(t, l)
	before, beforeSnap = stateOf(l)
	_, err = l.Deliver()
	wantErr(t, "Deliver(预取已满)", err, ErrPrefetchFull)
	wantStateUnchanged(t, l, before, beforeSnap)

	full := New(1)
	enqueueN(t, full, 1)
	mustDeliver(t, full)
	if got := full.Stats().Queued; got != 0 {
		t.Fatalf("queued = %d, want 0 (queue must be empty for the priority check)", got)
	}
	before, beforeSnap = stateOf(full)
	_, err = full.Deliver()
	wantErr(t, "Deliver(预取已满且队列为空)", err, ErrPrefetchFull)
	t.Logf("out: 预取已满与队列为空同时成立时报 %v (依据: 预取已满优先于队列为空)", ErrPrefetchFull)
	wantStateUnchanged(t, full, before, beforeSnap)
}

func TestReplayDeterminism(t *testing.T) {
	ops := buildScript(12345, 300)
	run := func() []string {
		l := New(3)
		var out []string
		for _, op := range ops {
			out = append(out, op.apply(l))
		}
		return out
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay mismatch:\nfirst:  %v\nsecond: %v", first, second)
	}
	t.Logf("out: 两次重放 %d 步调用序列输出完全一致 (依据: 相同调用序列重放得到完全相同的投递序列、标签与重投标志)", len(ops))
}

func TestConcurrentLinearizable(t *testing.T) {
	const (
		workers  = 8
		opsPer   = 500
		prefetch = 4
		maxEnq   = workers * opsPer
	)
	l := New(prefetch)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			ops := buildScript(seed, opsPer)
			for _, op := range ops {
				op.apply(l)
			}
		}(uint64(w*7919 + 1))
	}
	wg.Wait()

	stats, snap := stateOf(l)
	if stats.Unacked > prefetch {
		t.Fatalf("unacked = %d exceeds prefetch %d", stats.Unacked, prefetch)
	}
	seen := make(map[uint64]string)
	for _, seq := range snap.QueueSeqs {
		seen[seq] = "queued"
	}
	for _, seq := range snap.UnackedSeqs {
		if where, dup := seen[seq]; dup {
			t.Fatalf("seq %d in both %s and unacked", seq, where)
		}
		seen[seq] = "unacked"
	}
	for seq := range seen {
		if seq == 0 || seq > stats.MaxSeq {
			t.Fatalf("seq %d outside [1, %d]", seq, stats.MaxSeq)
		}
	}
	settled := int(stats.MaxSeq) - len(seen)
	if settled < 0 || uint64(settled) < stats.Dropped {
		t.Fatalf("settled = %d, dropped = %d: union invariant broken", settled, stats.Dropped)
	}
	if stats.MaxSeq > maxEnq {
		t.Fatalf("maxSeq = %d exceeds enqueued %d", stats.MaxSeq, maxEnq)
	}
	t.Logf("out: 并发后 stats=%+v (依据: 未确认数不超过P; 队列/未确认/已结算互不相交且并集为全部入队消息)", stats)
}
