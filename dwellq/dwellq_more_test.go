package dwellq

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// 入队超字节容量即尾丢；尾丢后队列与计数不变，腾出空间后可继续入队。
func TestTailDrop(t *testing.T) {
	const T, I, maxPkt int64 = 10, 100, 100
	m := mustNew(t, T, I, maxPkt, 250) // 容量 250 字节

	ok, tail := enq(t, m, 1, 100, 0)
	if !ok || tail {
		t.Fatalf("packet 1 should be accepted")
	}
	ok, tail = enq(t, m, 2, 100, 1)
	if !ok || tail {
		t.Fatalf("packet 2 should be accepted")
	}
	// 200 + 100 = 300 > 250 → 尾丢
	ok, tail = enq(t, m, 3, 100, 2)
	if ok || !tail {
		t.Fatalf("packet 3 should be tail-dropped")
	}
	// 51 字节：200+51=251 > 250 仍尾丢
	ok, tail = enq(t, m, 4, 51, 3)
	if ok || !tail {
		t.Fatalf("packet 4 (51B) should be tail-dropped: 200+51>250")
	}
	// 50 字节：恰好 250，入队成功
	ok, tail = enq(t, m, 5, 50, 4)
	if !ok || tail {
		t.Fatalf("packet 5 (50B) should be accepted: 200+50=250")
	}
	if s := m.Stats(); s.Enqueued != 3 || s.InBytes != 250 || s.InQueue != 3 {
		t.Fatalf("unexpected stats after tail drops: %+v", s)
	}
	// 出队一个包后腾出空间，尾丢场景解除。
	pkt, _, _ := deq(t, m, 5)
	if pkt.ID != 1 {
		t.Fatalf("expected packet 1 dequeued, got %d", pkt.ID)
	}
	ok, tail = enq(t, m, 6, 100, 6)
	if !ok || tail {
		t.Fatalf("packet 6 should be accepted after freeing space")
	}
	invariant(t, m)
}

// 构造参数校验：非正参数与 T>=I 给出可区分的错误。
func TestNewValidation(t *testing.T) {
	cases := []struct {
		name              string
		t, i, maxPkt, cap int64
		wantErr           error
	}{
		{"zero target", 0, 100, 100, 1000, ErrInvalidParam},
		{"negative interval", 10, -1, 100, 1000, ErrInvalidParam},
		{"zero max packet", 10, 100, 0, 1000, ErrInvalidParam},
		{"zero capacity", 10, 100, 100, 0, ErrInvalidParam},
		{"T equals I", 100, 100, 100, 1000, ErrTargetTooLarge},
		{"T greater than I", 200, 100, 100, 1000, ErrTargetTooLarge},
		{"valid", 10, 100, 100, 1000, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := New(c.t, c.i, c.maxPkt, c.cap)
			if c.wantErr == nil {
				if err != nil || m == nil {
					t.Fatalf("expected success, got err=%v m=%v", err, m)
				}
				return
			}
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("expected %v, got %v", c.wantErr, err)
			}
			if m != nil {
				t.Fatalf("manager must be nil on error")
			}
			logOp(t, fmt.Sprintf("New(%d,%d,%d,%d)", c.t, c.i, c.maxPkt, c.cap), "REJECTED: "+err.Error())
		})
	}
}

// 包长校验与时钟回拨：被拒绝的操作不得改变任何状态。
func TestPacketAndClockValidation(t *testing.T) {
	const T, I, maxPkt int64 = 10, 100, 100
	m := mustNew(t, T, I, maxPkt, 1000)

	for _, bad := range []int64{0, -5, 101} {
		ok, tail, _, err := m.Enqueue(Packet{ID: 99, Size: bad}, 1)
		if !errors.Is(err, ErrInvalidPacket) {
			t.Fatalf("size=%d: expected ErrInvalidPacket, got %v", bad, err)
		}
		if ok || tail {
			t.Fatalf("size=%d: rejected enqueue must report failure", bad)
		}
		logOp(t, fmt.Sprintf("Enqueue{id=99,size=%d,now=1}", bad), "REJECTED: "+err.Error())
	}
	before := m.Stats()

	// 入队接受一个包，随后时钟回拨必须被拒绝且状态不变。
	if _, tail, _, err := m.Enqueue(Packet{ID: 1, Size: 100}, 10); err != nil || tail {
		t.Fatalf("valid enqueue failed: %v", err)
	}
	_, _, _, err := m.Enqueue(Packet{ID: 2, Size: 100}, 9)
	if !errors.Is(err, ErrClockRewind) {
		t.Fatalf("expected ErrClockRewind on enqueue, got %v", err)
	}
	r := m.Dequeue(9)
	if !errors.Is(r.Err, ErrClockRewind) {
		t.Fatalf("expected ErrClockRewind on dequeue, got %v", r.Err)
	}
	logOp(t, "Dequeue{now=9}", "REJECTED: "+r.Err.Error())

	after := m.Stats()
	if before != (Stats{}) {
		t.Fatalf("state changed before valid op: %+v", before)
	}
	if after.Enqueued != 1 || after.InQueue != 1 {
		t.Fatalf("rejected operations changed state: %+v", after)
	}
	// 回拨被拒绝后，使用正常时刻仍可工作（lastTime 未被污染）。
	pkt, _, _ := deq(t, m, 11)
	if pkt.ID != 1 {
		t.Fatalf("expected packet 1 after rewind rejection, got %d", pkt.ID)
	}
	invariant(t, m)
}

// 并发调用：恒有 入队成功数 == 出队数 + 主动丢包数 + 队内包数。
func TestConcurrentInvariant(t *testing.T) {
	const T, I, maxPkt int64 = 5, 50, 100
	m := mustNew(t, T, I, maxPkt, 2000)

	const producers, perP, consumers = 8, 200, 8
	var wg sync.WaitGroup
	clock := int64(0)
	var clockMu sync.Mutex
	// 时间戳的领取与调用必须原子：否则 goroutine A 先领到较小时刻却晚于
	// 领到较晚时刻的 B 执行，会被（正确地）判为时钟回拨。
	timedEnqueue := func(p Packet) (int64, error) {
		clockMu.Lock()
		defer clockMu.Unlock()
		clock++
		now := clock
		_, _, reason, err := m.Enqueue(p, now)
		if err == nil {
			logOp(t, fmt.Sprintf("Enqueue{id=%d,now=%d}", p.ID, now), reason)
		}
		return now, err
	}
	timedDequeue := func() (int64, DequeueResult) {
		clockMu.Lock()
		defer clockMu.Unlock()
		clock++
		now := clock
		return now, m.Dequeue(now)
	}

	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(pid int) {
			defer wg.Done()
			for j := 0; j < perP; j++ {
				_, err := timedEnqueue(Packet{ID: uint64(pid*perP + j + 1), Size: 100})
				if err != nil {
					t.Errorf("concurrent enqueue err: %v", err)
					return
				}
			}
		}(p)
	}
	for c := 0; c < consumers; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perP; j++ {
				_, r := timedDequeue()
				if r.Err != nil {
					t.Errorf("concurrent dequeue err: %v", r.Err)
					return
				}
				_ = r
			}
		}()
	}
	wg.Wait()

	s := m.Stats()
	t.Logf("final stats: %+v", s)
	if s.Enqueued != s.Dequeued+s.Dropped+s.InQueue {
		t.Fatalf("concurrent invariant violated: %d != %d+%d+%d",
			s.Enqueued, s.Dequeued, s.Dropped, s.InQueue)
	}
	if s.InBytes != s.InQueue*100 {
		t.Fatalf("byte accounting mismatch: inBytes=%d inQueue=%d", s.InBytes, s.InQueue)
	}
}

// 相同的入队、出队与时钟序列重放，必须得到相同的丢包序列（纯确定性）。
func TestReplayDeterminism(t *testing.T) {
	type op struct {
		enq  bool
		id   uint64
		size int64
		now  int64
	}
	// 固定脚本：交替入队/出队，制造从超标到进入丢弃状态的过程。
	var script []op
	now := int64(0)
	for i := int64(1); i <= 30; i++ {
		script = append(script, op{enq: true, id: uint64(i), size: 100, now: now})
	}
	for t0 := int64(1); t0 <= 300; t0 += 3 {
		script = append(script, op{enq: false, now: t0})
		if t0%2 == 0 {
			now = t0
			script = append(script, op{enq: true, id: uint64(1000 + t0), size: 100, now: now})
		}
	}

	type runResult struct {
		returned []uint64
		dropped  []uint64
		empties  int
		tail     int
	}
	run := func() runResult {
		m := mustNew(t, 10, 100, 100, 100000)
		var res runResult
		for _, o := range script {
			if o.enq {
				ok, tail, _, err := m.Enqueue(Packet{ID: o.id, Size: o.size}, o.now)
				if err != nil {
					t.Fatalf("replay enqueue err: %v", err)
				}
				if !ok && tail {
					res.tail++
				}
				continue
			}
			r := m.Dequeue(o.now)
			if r.Err != nil {
				t.Fatalf("replay dequeue err: %v", r.Err)
			}
			if r.Empty {
				res.empties++
			}
			if r.Packet != nil {
				res.returned = append(res.returned, r.Packet.ID)
			}
			for _, p := range r.Dropped {
				res.dropped = append(res.dropped, p.ID)
			}
		}
		return res
	}

	first := run()
	for k := 0; k < 3; k++ {
		got := run()
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("replay %d differs:\n first=%+v\n got  =%+v", k+1, first, got)
		}
	}
	t.Logf("deterministic drop sequence: %v", first.dropped)
	if len(first.dropped) == 0 {
		t.Fatalf("script should have produced at least one drop at times up to 300")
	}
}
