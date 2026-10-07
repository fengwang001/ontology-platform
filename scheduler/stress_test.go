package scheduler_test

import (
	"math/rand"
	"sync"
	"testing"

	"ontology/scheduler"
)

// runWorkload 以确定性伪随机事件流驱动调度器，返回操作计数。
func runWorkload(t *testing.T, events int, seed int64) scheduler.Stats {
	t.Helper()
	s, err := scheduler.New(100)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rng := rand.New(rand.NewSource(seed))
	ids := []string{"w0", "w1", "w2", "w3"}
	added := map[string]bool{}
	for i := 0; i < events; i++ {
		ev := genEvent(rng, ids, added, s.Snapshot())
		if _, err := ev.applyReal(s); err != nil {
			// 被拒绝的事件同样计入开销，属正常路径
			continue
		}
	}
	return s.Stats()
}

func workUnits(st scheduler.Stats) int64 {
	return st.PickCalls + st.TreapInserts + st.TreapRemovals +
		st.FreshPushes + st.FreshPops + st.InflightPushes + st.InflightPops
}

// TestComplexityInvariants 验证每个基本操作的计数都被事件数与
// 段生命周期次数线性 bound，从而单事件均摊开销不随队列中待发
// 数据量或历史事件数增长（treap 内部为期望对数，见 DESIGN.md）。
func TestComplexityInvariants(t *testing.T) {
	st := runWorkload(t, 30000, 42)

	if st.PickCalls > st.SegmentsSent+st.Events {
		t.Fatalf("选路调用越界：PickCalls=%d > Sent=%d + Events=%d",
			st.PickCalls, st.SegmentsSent, st.Events)
	}
	if st.TreapInserts > st.SegmentsEvicted {
		t.Fatalf("treap 插入多于失效清除：%d > %d", st.TreapInserts, st.SegmentsEvicted)
	}
	if st.SegmentsEvicted > st.SegmentsSent {
		t.Fatalf("失效清除多于发送：%d > %d", st.SegmentsEvicted, st.SegmentsSent)
	}
	if st.TreapRemovals != st.SegmentsResent+st.SegmentsDropped {
		t.Fatalf("treap 删除不守恒：%d != %d + %d",
			st.TreapRemovals, st.SegmentsResent, st.SegmentsDropped)
	}
	if st.TreapRemovals > st.TreapInserts {
		t.Fatalf("treap 删除多于插入：%d > %d", st.TreapRemovals, st.TreapInserts)
	}
	if st.InflightPushes != st.SegmentsSent {
		t.Fatalf("在途入队应等于发送次数：%d != %d", st.InflightPushes, st.SegmentsSent)
	}
	if st.InflightPops != st.SegmentsReleased+st.SegmentsEvicted {
		t.Fatalf("在途出队不守恒：%d != %d + %d",
			st.InflightPops, st.SegmentsReleased, st.SegmentsEvicted)
	}
	if st.InflightPops > st.InflightPushes {
		t.Fatalf("在途出队多于入队：%d > %d", st.InflightPops, st.InflightPushes)
	}
	if st.FreshPushes != st.SegmentsWritten {
		t.Fatalf("新段入队应等于写入段数：%d != %d", st.FreshPushes, st.SegmentsWritten)
	}
	if st.FreshPops > st.FreshPushes {
		t.Fatalf("新段出队多于入队：%d > %d", st.FreshPops, st.FreshPushes)
	}

	w := workUnits(st)
	bound := 5 * (st.Events + st.SegmentsWritten + st.SegmentsSent)
	if w > bound {
		t.Fatalf("基本操作总量超线性界：%d > 5*(%d+%d+%d)",
			w, st.Events, st.SegmentsWritten, st.SegmentsSent)
	}
	t.Logf("事件=%d 写入段=%d 发送段=%d 基本操作=%d 上界=%d treap节点访问=%d",
		st.Events, st.SegmentsWritten, st.SegmentsSent, w, bound, st.TreapNodeVisits)
}

// TestComplexityScaling 以 4 倍事件数验证基本操作总量近线性增长
// （留足对数因子余量），作为复杂度声明的经验性佐证。
func TestComplexityScaling(t *testing.T) {
	const n = 20000
	s1 := runWorkload(t, n, 7)
	s2 := runWorkload(t, 4*n, 7)

	w1, w2 := workUnits(s1), workUnits(s2)
	if w1 == 0 || w2 > 8*w1 {
		t.Fatalf("基本操作增长超线性：N=%d 时 %d，4N 时 %d", n, w1, w2)
	}
	if s1.TreapNodeVisits > 0 && s2.TreapNodeVisits > 16*s1.TreapNodeVisits {
		t.Fatalf("treap 节点访问增长超线性对数界：%d -> %d", s1.TreapNodeVisits, s2.TreapNodeVisits)
	}
	t.Logf("N=%d: 基本操作=%d treap访问=%d；4N: 基本操作=%d treap访问=%d",
		n, w1, s1.TreapNodeVisits, w2, s2.TreapNodeVisits)
}

// TestConcurrentEvents 并发施加事件（配合 -race 运行），
// 验证串行等价性下的状态不变量始终成立。
func TestConcurrentEvents(t *testing.T) {
	s, err := scheduler.New(100)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ids := []string{"g0", "g1", "g2", "g3"}
	for _, id := range ids {
		if _, err := s.AddSubflow(id, 10, 10000, scheduler.Normal); err != nil {
			t.Fatalf("AddSubflow: %v", err)
		}
	}
	if _, err := s.ConnAck(0, 1<<40); err != nil {
		t.Fatalf("ConnAck: %v", err)
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 2000; i++ {
				id := ids[rng.Intn(len(ids))]
				switch rng.Intn(6) {
				case 0:
					_, _ = s.Write(1 + rng.Int63n(300))
				case 1:
					_, _ = s.SubflowAck(id, rng.Int63n(1<<30))
				case 2:
					_, _ = s.ConnAck(rng.Int63n(1<<30), rng.Int63n(1<<20))
				case 3:
					_, _ = s.SubflowFail(id)
				case 4:
					_, _ = s.SubflowRecover(id)
				case 5:
					_, _ = s.SetRTT(id, 1+rng.Int63n(50))
				}
			}
		}(int64(g))
	}
	wg.Wait()

	st := s.Snapshot()
	if !(st.Acked <= st.SentMax && st.SentMax <= st.NextSeq) {
		t.Fatalf("序号不变量破坏：acked=%d sentMax=%d nextSeq=%d", st.Acked, st.SentMax, st.NextSeq)
	}
	prev := int64(-1)
	for _, g := range st.PendingReinject {
		if g.Seq <= prev {
			t.Fatalf("待发重新注入段未按序号升序或无去重：%+v", st.PendingReinject)
		}
		prev = g.Seq
		if g.Seq+g.Len > st.SentMax {
			t.Fatalf("重新注入段超出已发送范围：%+v", g)
		}
		if g.Seq+g.Len <= st.Acked {
			t.Fatalf("被连接级确认覆盖的重新注入段未被丢弃：%+v", g)
		}
	}
	cursor := st.SentMax
	for _, g := range st.PendingFresh {
		if g.Seq != cursor {
			t.Fatalf("待发新数据段不连续：%+v（期望起点 %d）", g, cursor)
		}
		cursor = g.Seq + g.Len
	}
	if cursor != st.NextSeq {
		t.Fatalf("待发新数据段未覆盖到写入位置：%d != %d", cursor, st.NextSeq)
	}
	for _, sf := range st.Subflows {
		var sum int64
		for _, g := range sf.Inflight {
			sum += g.Len
			if g.Seq+g.Len > st.SentMax {
				t.Fatalf("子流 %q 在途段超出已发送范围：%+v", sf.ID, g)
			}
		}
		if sum != sf.InflightBytes {
			t.Fatalf("子流 %q 在途字节数不符：%d != %d", sf.ID, sum, sf.InflightBytes)
		}
		if sf.AckedBytes+sf.InflightBytes > sf.SentBytes {
			t.Fatalf("子流 %q 确认+在途超过发送：%d+%d > %d",
				sf.ID, sf.AckedBytes, sf.InflightBytes, sf.SentBytes)
		}
		if !sf.Active && (sf.InflightBytes != 0 || len(sf.Inflight) != 0) {
			t.Fatalf("失效子流 %q 在途未清零：%+v", sf.ID, sf)
		}
	}
}
