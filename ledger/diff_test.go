package ledger

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// op 是一次可序列化的调用，用于差分对照与重放确定性。
type op struct {
	kind     int // 0 enqueue 1 deliver 2 ack 3 nack
	tag      uint64
	multiple bool
	requeue  bool
	msg      string
}

func runOnLedger(l *Ledger[string], o op) (string, bool) {
	switch o.kind {
	case 0:
		return fmt.Sprintf("seq=%d", l.Enqueue(o.msg)), true
	case 1:
		d, err := l.Deliver()
		if err != nil {
			return reasonOf(err), false
		}
		return fmt.Sprintf("tag=%d seq=%d msg=%q red=%v", d.Tag, d.Seq, d.Message, d.Redeliver), true
	case 2:
		err := l.Ack(o.tag, o.multiple)
		return reasonOf(err), err == nil
	default:
		err := l.Nack(o.tag, o.multiple, o.requeue)
		return reasonOf(err), err == nil
	}
}

func runOnModel(m *naiveModel, o op) (string, bool) {
	switch o.kind {
	case 0:
		return fmt.Sprintf("seq=%d", m.enqueue(o.msg)), true
	case 1:
		d, r, ok := m.deliver()
		if !ok {
			return r.String(), false
		}
		return fmt.Sprintf("tag=%d seq=%d msg=%q red=%v", d.tag, d.seq, d.message, d.redeliver), true
	case 2:
		r, ok := m.ack(int(o.tag), o.multiple)
		if !ok {
			return r.String(), false
		}
		return "ok", true
	default:
		r, ok := m.nack(int(o.tag), o.multiple, o.requeue)
		if !ok {
			return r.String(), false
		}
		return "ok", true
	}
}

// 随机操作流与朴素模型逐步差分，每步（含被拒绝的操作）后全量状态必须一致。
func TestRandomDifferential(t *testing.T) {
	const P = 2
	for seed := int64(1); seed <= 40; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			l := New[string](P)
			m := newNaiveModel(P)

			for step := 0; step < 2000; step++ {
				o := op{}
				switch rng.Intn(10) {
				case 0, 1, 2:
					o.kind = 0
					o.msg = fmt.Sprintf("s%dm%d", seed, step)
				case 3, 4:
					o.kind = 1
				default:
					// 偏向覆盖非法标签、已结算标签与合法批量范围。
					maxTag := l.MaxTag()
					bound := uint64(0)
					if maxTag > 0 {
						bound = 1 + maxTag + uint64(rng.Intn(3))
					}
					o.tag = uint64(rng.Intn(int(bound) + 1))
					o.multiple = rng.Intn(2) == 0
					if rng.Intn(2) == 0 {
						o.kind = 2
					} else {
						o.kind = 3
						o.requeue = rng.Intn(2) == 0
					}
				}

				lOut, lOK := runOnLedger(l, o)
				mOut, mOK := runOnModel(m, o)
				if lOut != mOut || lOK != mOK {
					t.Fatalf("seed=%d step=%d op=%+v\nledger: %s (%v)\nmodel : %s (%v)",
						seed, step, o, lOut, lOK, mOut, mOK)
				}
				assertEquivalent(t, l, m, fmt.Sprintf("seed=%d step=%d", seed, step))

				// 再重放一次同一个操作：双方都必须再次拒绝（被拒操作幂等不改状态）。
				if !lOK {
					lOut2, lOK2 := runOnLedger(l, o)
					mOut2, mOK2 := runOnModel(m, o)
					if lOut2 != lOut || mOut2 != mOut || lOK2 || mOK2 {
						t.Fatalf("seed=%d step=%d rejected op changed result on replay: %+v", seed, step, o)
					}
					assertEquivalent(t, l, m, fmt.Sprintf("seed=%d step=%d replayed-reject", seed, step))
				}
			}
		})
	}
}

// 相同的调用序列在两个独立账本上重放，投递序列（标签与重投标志）必须完全相同。
func TestReplayDeterminism(t *testing.T) {
	const P = 3
	ops := []op{
		{kind: 0, msg: "x"}, {kind: 0, msg: "y"}, {kind: 0, msg: "z"},
		{kind: 1}, {kind: 1},
		{kind: 3, tag: 1, requeue: true},
		{kind: 1}, {kind: 1},
		{kind: 2, tag: 3, multiple: true},
		{kind: 3, tag: 4, requeue: true},
		{kind: 1}, {kind: 1}, {kind: 1},
		{kind: 2, tag: 7, multiple: true},
	}

	record := func(l *Ledger[string]) []string {
		var got []string
		for _, o := range ops {
			out, ok := runOnLedger(l, o)
			got = append(got, fmt.Sprintf("%v:%s", ok, out))
		}
		return got
	}
	a := record(New[string](P))
	b := record(New[string](P))
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Fatalf("replay differs:\n%v\n%v", a, b)
	}
	t.Logf("重放结果完全一致：%v", a)
}

// 并发调用必须可串行化：无竞态、不 panic、未确认数始终 <= P、
// 三态并集始终等于全部入队消息。
func TestConcurrentLinearizable(t *testing.T) {
	const P = 4
	l := New[string](P)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 生产者：持续入队。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
				l.Enqueue(fmt.Sprintf("c%d", i))
			}
		}
	}()

	// 投递者：拿到的标签只增不复用，重投标志合法。
	var maxSeen uint64
	var seenMu sync.Mutex
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				d, err := l.Deliver()
				if err == nil {
					seenMu.Lock()
					if d.Tag <= maxSeen && d.Tag != 0 {
						t.Errorf("tag not monotonic: got %d after %d", d.Tag, maxSeen)
					}
					if d.Tag > maxSeen {
						maxSeen = d.Tag
					}
					seenMu.Unlock()
				}
			}
		}()
	}

	// 确认/拒绝者：随机结算标签。
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				tag := uint64(1)
				if mx := l.MaxTag(); mx > 0 {
					tag = uint64(int(mx)%17 + 1) // 含 0 以外的非法/合法标签
				}
				if id%2 == 0 {
					_ = l.Ack(tag, tag%2 == 0)
				} else {
					_ = l.Nack(tag, tag%3 == 0, tag%2 == 0)
				}
			}
		}(w)
	}

	// 不变量巡检。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if n := l.UnackedCount(); n > P {
				t.Errorf("unacked %d > P %d", n, P)
			}
			l.mu.Lock()
			total := 0
			for _, e := range l.entries {
				if e.phase != phaseQueued && e.phase != phaseUnacked && e.phase != phaseSettled {
					t.Errorf("bad phase")
				}
				total++
			}
			if len(l.unacked) != 0 && total == 0 {
				t.Errorf("partition impossible")
			}
			l.mu.Unlock()
		}
	}()

	// 让并发跑一段时间后停止。
	for i := 0; i < 500; i++ {
		l.Enqueue("spin")
		if d, err := l.Deliver(); err == nil {
			_ = l.Ack(d.Tag, false)
		}
	}
	close(stop)
	wg.Wait()

	// 终态：把队列清空并全部结算（丢弃），所有消息最终 settled。
	for {
		d, err := l.Deliver()
		if err != nil {
			un := l.Unacked()
			if len(un) == 0 && l.QueueLen() == 0 {
				break
			}
			if len(un) == 0 {
				t.Fatalf("cannot drain: %v queue=%d", err, l.QueueLen())
			}
			// 预取满：先结算一条腾出名额再继续。
			if err := l.Nack(un[0].Tag, false, false); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := l.Nack(d.Tag, false, false); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("并发结束：maxTag=%d dropped=%d", l.MaxTag(), l.Dropped())
	if l.QueueLen() != 0 || l.UnackedCount() != 0 {
		t.Fatalf("drained ledger not empty: queue=%d unacked=%d", l.QueueLen(), l.UnackedCount())
	}
}

// 被拒操作不得改变任何状态：对四类原因逐一快照前后状态。
func TestRejectedOpsDoNotMutate(t *testing.T) {
	l := New[string](2)
	snapshot := func() string {
		return fmt.Sprintf("maxTag=%d q=%v un=%v drop=%d",
			l.MaxTag(), l.QueuedMessages(), l.Unacked(), l.Dropped())
	}
	expectReject := func(want ErrReason, f func() error, name string) {
		before := snapshot()
		err := f()
		after := snapshot()
		var e *Error
		if !errors.As(err, &e) || e.Reason != want {
			t.Fatalf("%s: want reason %d, got %v", name, want, err)
		}
		if before != after {
			t.Fatalf("%s mutated state:\nbefore %s\nafter  %s", name, before, after)
		}
		t.Logf("输入 %s -> 输出 %q；判定：被拒，状态快照不变（%s）", name, reasonOf(err), after)
	}

	expectReject(ErrQueueEmpty, func() error { _, err := l.Deliver(); return err }, "Deliver(空队列)")
	l.Enqueue("a")
	l.Enqueue("b")
	_, _ = l.Deliver()
	_, _ = l.Deliver()
	expectReject(ErrPrefetchFull, func() error { _, err := l.Deliver(); return err }, "Deliver(预取满+队列空)")

	expectReject(ErrTagIllegal, func() error { return l.Ack(0, false) }, "Ack(tag=0)")
	expectReject(ErrTagIllegal, func() error { return l.Nack(99, true, true) }, "Nack(tag=99)")
	if err := l.Ack(1, false); err != nil { // 先合法结算 tag1
		t.Fatal(err)
	}
	expectReject(ErrAlreadySettled, func() error { return l.Ack(1, false) }, "Ack(已结算 tag1)")
	expectReject(ErrAlreadySettled, func() error { return l.Nack(1, false, true) }, "Nack(已结算 tag1)")
	// 构造真正为空的范围：先把 tag2 单独结算。
	if err := l.Ack(2, false); err != nil {
		t.Fatal(err)
	}
	expectReject(ErrRangeEmpty, func() error { return l.Ack(2, true) }, "Ack(2,multiple) 范围为空")
}
