package dwellq

import (
	"fmt"
	"testing"
)

// logOp 打印一次操作的输入、输出与判定依据（t.Log 仅在 -v 或失败时输出）。
func logOp(t *testing.T, input, output string) {
	t.Helper()
	t.Logf("INPUT : %s\nOUTPUT: %s", input, output)
}

func mustNew(t *testing.T, target, interval, maxPacketSize, byteCapacity int64) *Manager {
	t.Helper()
	m, err := New(target, interval, maxPacketSize, byteCapacity)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d) unexpected error: %v", target, interval, maxPacketSize, byteCapacity, err)
	}
	return m
}

// invariant 校验：入队成功数 == 出队数 + 主动丢包数 + 队内包数。
func invariant(t *testing.T, m *Manager) {
	t.Helper()
	s := m.Stats()
	if got := s.Dequeued + s.Dropped + s.InQueue; got != s.Enqueued {
		t.Fatalf("invariant violated: enqueued=%d != dequeued=%d + dropped=%d + inQueue=%d (=%d)",
			s.Enqueued, s.Dequeued, s.Dropped, s.InQueue, got)
	}
}

func enq(t *testing.T, m *Manager, id uint64, size, now int64) (bool, bool) {
	t.Helper()
	ok, tail, reason, err := m.Enqueue(Packet{ID: id, Size: size}, now)
	if err != nil {
		t.Fatalf("enqueue id=%d unexpected error: %v", id, err)
	}
	logOp(t, fmt.Sprintf("Enqueue{id=%d,size=%d,now=%d}", id, size, now), reason)
	invariant(t, m)
	return ok, tail
}

func deq(t *testing.T, m *Manager, now int64) (*Packet, []Packet, bool) {
	t.Helper()
	r := m.Dequeue(now)
	if r.Err != nil {
		t.Fatalf("dequeue now=%d unexpected error: %v", now, r.Err)
	}
	id := uint64(0)
	if r.Packet != nil {
		id = r.Packet.ID
	}
	dropIDs := make([]uint64, 0, len(r.Dropped))
	for _, p := range r.Dropped {
		dropIDs = append(dropIDs, p.ID)
	}
	logOp(t, fmt.Sprintf("Dequeue{now=%d}", now),
		fmt.Sprintf("returned=%v dropped=%v empty=%v | %s", id, dropIDs, r.Empty, r.Reason))
	invariant(t, m)
	return r.Packet, r.Dropped, r.Empty
}

// 超标但持续不足一个观察期的短暂突发：不丢包；未超标时计时器被清除，
// 之后再次超标需要重新观察一个完整 I。
func TestShortBurstUnderOneIntervalNotDropped(t *testing.T) {
	const T, I, maxPkt int64 = 10, 100, 100
	m := mustNew(t, T, I, maxPkt, 100000)

	enq(t, m, 1, 100, 0)
	enq(t, m, 2, 100, 1)
	enq(t, m, 3, 100, 2)

	// now=50：dwell=50>=10，放行后剩余 200 > 100 → 超标，首次设置 firstAbove=150，包1放行。
	pkt, dropped, empty := deq(t, m, 50)
	if empty || pkt == nil || pkt.ID != 1 || len(dropped) != 0 {
		t.Fatalf("expected packet 1 returned with no drops, got pkt=%v dropped=%d empty=%v", pkt, len(dropped), empty)
	}
	if s := m.State(); s.FirstAbove != 150 {
		t.Fatalf("expected firstAbove=150, got %+v", s)
	}

	// now=60：包2 放行后只剩包3（100 字节），100 <= maxPacket=100 → 未超标，清除 firstAbove。
	pkt, _, _ = deq(t, m, 60)
	if pkt == nil || pkt.ID != 2 {
		t.Fatalf("expected packet 2 returned, got %v", pkt)
	}
	if s := m.State(); s.FirstAbove != 0 {
		t.Fatalf("expected firstAbove cleared, got %d", s.FirstAbove)
	}

	// now=160（距首次超标已远超 I，但计时器曾被清除）再次超标：
	// 必须重新计时（firstAbove=260），而不是立即丢包。
	enq(t, m, 4, 100, 61)
	enq(t, m, 5, 100, 62)
	pkt, dropped, _ = deq(t, m, 160)
	if pkt == nil || pkt.ID != 3 || len(dropped) != 0 {
		t.Fatalf("expected packet 3 returned after timer reset, got pkt=%v drops=%d", pkt, len(dropped))
	}
	if s := m.State(); s.FirstAbove != 260 {
		t.Fatalf("expected firstAbove reset to 260, got %d", s.FirstAbove)
	}
}

// 逗留恰等于 T 属于超标（>= T）；逗留 T-1 未超标并清除计时器。
func TestDwellExactlyTargetIsOverTarget(t *testing.T) {
	const T, I, maxPkt int64 = 10, 100, 100
	m := mustNew(t, T, I, maxPkt, 100000)
	enq(t, m, 1, 100, 0)
	enq(t, m, 2, 100, 0)
	enq(t, m, 3, 100, 0)

	pkt, dropped, _ := deq(t, m, 10)
	if pkt == nil || pkt.ID != 1 || len(dropped) != 0 {
		t.Fatalf("expected packet 1 returned (timer just armed), got pkt=%v drops=%d", pkt, len(dropped))
	}
	if s := m.State(); s.FirstAbove != 110 {
		t.Fatalf("expected firstAbove=110, got %d", s.FirstAbove)
	}

	// 包2 在 2 时刻入队、11 时刻出队：dwell=9=T-1 < T → 未超标，清除计时器。
	// 先排空旧包：重新构造一个管理器更直接。
	m2 := mustNew(t, T, I, maxPkt, 100000)
	enq(t, m2, 1, 100, 2)
	enq(t, m2, 2, 100, 3) // 保证放行包1后剩余 100 不超过 maxPacket
	pkt2, _, _ := deq(t, m2, 11)
	if pkt2 == nil || pkt2.ID != 1 {
		t.Fatalf("expected packet 1 returned, got %v", pkt2)
	}
	if s := m2.State(); s.FirstAbove != 0 {
		t.Fatalf("expected firstAbove cleared when dwell<T, got %d", s.FirstAbove)
	}
}

// 首次丢包恰在 I 之后：now == firstAbove（首次超标时刻 + I）即丢弃并进入丢弃状态。
func TestFirstDropExactlyAfterInterval(t *testing.T) {
	const T, I, maxPkt int64 = 10, 100, 100
	m := mustNew(t, T, I, maxPkt, 100000)
	// 6 个包：保证丢包瞬间以及随后放行时，出队后剩余字节都大于一个最大包长。
	for id := int64(1); id <= 6; id++ {
		enq(t, m, uint64(id), 100, 0)
	}

	pkt, _, _ := deq(t, m, 10) // 首次超标，firstAbove=110
	if pkt.ID != 1 {
		t.Fatalf("expected packet 1, got %d", pkt.ID)
	}
	pkt, _, _ = deq(t, m, 109) // 尚未到 110
	if pkt.ID != 2 {
		t.Fatalf("expected packet 2 at now=109, got %d", pkt.ID)
	}
	if m.Dropping() {
		t.Fatalf("must not be dropping before firstAbove")
	}

	// now=110 恰等于 firstAbove：丢弃包3、进入丢弃状态；
	// count=1，nextDrop=210，now=110 < 210，故包4 放行。
	pkt, dropped, _ := deq(t, m, 110)
	if pkt == nil || pkt.ID != 4 {
		t.Fatalf("expected packet 4 returned after dropping packet 3, got %v", pkt)
	}
	if len(dropped) != 1 || dropped[0].ID != 3 {
		t.Fatalf("expected exactly packet 3 dropped, got %v", dropped)
	}
	if !m.Dropping() {
		t.Fatalf("expected to enter dropping state")
	}
	if s := m.State(); s.Count != 1 || s.NextDrop != 210 {
		t.Fatalf("expected count=1 nextDrop=210, got %+v", s)
	}
}

// 丢包间隔按平方根收缩：I, I/√2, I/√3 ...
func TestDropIntervalsSqrtShrink(t *testing.T) {
	const T, I, maxPkt int64 = 10, 100, 100
	m := mustNew(t, T, I, maxPkt, 100000)

	wantD := map[int64]int64{1: 100, 2: 70, 3: 57, 4: 50, 5: 44, 6: 40}
	for c, want := range wantD {
		got := controlLaw(I, c)
		if got != want {
			t.Fatalf("controlLaw(%d)=%d, want %d (满足 d^2*count <= I^2 的最大整数)", c, got, want)
		}
		if got*got*c > I*I {
			t.Fatalf("controlLaw(%d)=%d violates d^2*c <= I^2", c, got)
		}
	}

	// 足够多的包，保证每次出队后剩余字节始终 > maxPacket，只考察时间维度。
	for id := int64(1); id <= 20; id++ {
		enq(t, m, uint64(id), 100, 0)
	}

	deq(t, m, 200) // 包1：首次超标武装 firstAbove=300
	pkt, dropped, _ := deq(t, m, 300)
	if len(dropped) != 1 || dropped[0].ID != 2 || pkt.ID != 3 {
		t.Fatalf("drop 2 / return 3 expected, got dropped=%v pkt=%d", dropped, pkt.ID)
	}
	pkt, dropped, _ = deq(t, m, 399) // 未到 nextDrop=400
	if len(dropped) != 0 || pkt.ID != 4 {
		t.Fatalf("at 399 expect packet 4 returned, got pkt=%d drops=%d", pkt.ID, len(dropped))
	}
	pkt, dropped, _ = deq(t, m, 400) // 丢包5（间隔100=I/√1），count=2，nextDrop=470
	if len(dropped) != 1 || dropped[0].ID != 5 || pkt.ID != 6 {
		t.Fatalf("drop 5 / return 6 expected, got dropped=%v pkt=%d", dropped, pkt.ID)
	}
	if s := m.State(); s.Count != 2 || s.NextDrop != 470 {
		t.Fatalf("expected count=2 nextDrop=470, got %+v", s)
	}
	pkt, dropped, _ = deq(t, m, 470) // 丢包7（间隔70=I/√2），count=3，nextDrop=527
	if len(dropped) != 1 || dropped[0].ID != 7 || pkt.ID != 8 {
		t.Fatalf("drop 7 / return 8 expected, got dropped=%v pkt=%d", dropped, pkt.ID)
	}
	if s := m.State(); s.Count != 3 || s.NextDrop != 527 {
		t.Fatalf("expected count=3 nextDrop=527, got %+v", s)
	}
}

// 16 倍 I 内重入丢弃状态：计数取上次退出计数减 2；超过 16I 则重置为 1。
func TestReentryWithin16IntervalsReusesCount(t *testing.T) {
	const T, I, maxPkt int64 = 10, 100, 100
	m := mustNew(t, T, I, maxPkt, 100000)

	for id := int64(1); id <= 16; id++ {
		enq(t, m, uint64(id), 100, 0)
	}
	// 第一轮：200 武装 → 300 丢2(count1,next400) → 400 丢3(count2,next470)
	// → 470 丢4(count3,next527) → 527 丢5(count4,next577) → 577 丢6(count5,next621)。
	// 每次出队调用丢 1 个并放行紧随其后的 1 个。
	deq(t, m, 200)
	deq(t, m, 300)
	deq(t, m, 400)
	deq(t, m, 470)
	deq(t, m, 527)
	deq(t, m, 577)
	if s := m.State(); s.Count != 5 || s.NextDrop != 621 {
		t.Fatalf("expected count=5 nextDrop=621 after first round, got %+v", s)
	}

	// 在 nextDrop=621 之前的 620 排空剩余旧包（同刻不会再丢）。
	// 旧包排空时因「队空」退出丢弃状态，并保存退出时计数 count=5。
	for {
		r := m.Dequeue(620)
		if r.Err != nil {
			t.Fatalf("unexpected err during drain: %v", r.Err)
		}
		logOp(t, "Dequeue{now=620} drain-first-round", r.Reason)
		if r.Empty {
			break
		}
	}
	if s := m.State(); !s.HasLastExit || s.LastExitCnt != 5 {
		t.Fatalf("expected saved exit count=5, got %+v", s)
	}
	// 新鲜包：入队即出队，dwell=0 < T（空队列上的普通操作）。
	enq(t, m, 100, 100, 621)
	pkt, _, _ := deq(t, m, 621)
	if pkt.ID != 100 || m.Dropping() {
		t.Fatalf("expected fresh packet 100, pkt=%d dropping=%v", pkt.ID, m.Dropping())
	}

	// 第二轮（16I=1600 之内）：700 武装，800 首次丢包时计数取 5-2=3，
	// nextDrop = 800 + I/√3 = 857。
	enq(t, m, 101, 100, 700)
	enq(t, m, 102, 100, 700)
	enq(t, m, 103, 100, 700)
	enq(t, m, 104, 100, 700)
	enq(t, m, 105, 100, 700)
	enq(t, m, 106, 100, 700)
	deq(t, m, 700)                      // 包101：dwell=0<T 未超标，放行（剩余500字节）
	deq(t, m, 800)                      // 包102：dwell=100>=10，剩余400>100 超标，武装 firstAbove=900
	pkt, reDropped, _ := deq(t, m, 900) // 丢103，进入丢弃状态，count=3，nextDrop=957；900<957 放行104
	if len(reDropped) != 1 || reDropped[0].ID != 103 || pkt.ID != 104 {
		t.Fatalf("reentry: drop 103 / return 104 expected, got dropped=%v pkt=%d", reDropped, pkt.ID)
	}
	if s := m.State(); s.Count != 3 || s.NextDrop != 957 {
		t.Fatalf("expected reused count=3 (5-2), nextDrop=957, got %+v", s)
	}

	// 超过 16I 后再重入：用独立管理器，先造一次退出（count=5），
	// 越过 16I=1600 后再次进入，计数必须重置为 1。
	m2 := mustNew(t, T, I, maxPkt, 100000)
	for id := int64(1); id <= 20; id++ {
		enq(t, m2, uint64(id), 100, 0)
	}
	deq(t, m2, 200)
	deq(t, m2, 300)
	deq(t, m2, 400)
	deq(t, m2, 470)
	deq(t, m2, 527)
	deq(t, m2, 577) // count=5, nextDrop=621
	for {           // 620 排空旧包 → 队空退出，保存 count=5
		r := m2.Dequeue(620)
		if r.Err != nil {
			t.Fatalf("drain err: %v", r.Err)
		}
		logOp(t, "m2 Dequeue{now=620} drain", r.Reason)
		if r.Empty {
			break
		}
	}
	enq(t, m2, 201, 100, 2300)
	enq(t, m2, 202, 100, 2300)
	enq(t, m2, 203, 100, 2300)
	enq(t, m2, 204, 100, 2300)
	enq(t, m2, 205, 100, 2300)
	enq(t, m2, 206, 100, 2300)
	enq(t, m2, 207, 100, 2300)
	deq(t, m2, 2300) // 包201 dwell=0<T 未超标
	deq(t, m2, 2350) // 包202 dwell=50>=10，剩余200>100 超标，武装 firstAbove=2450
	pkt2, drops2, _ := deq(t, m2, 2400)
	if pkt2 == nil || pkt2.ID != 203 || len(drops2) != 0 {
		t.Fatalf("far reentry at 2400: expect 203 returned, got pkt=%d drops=%d", pkt2.ID, len(drops2))
	}
	pkt2, drops2, _ = deq(t, m2, 2450)
	if len(drops2) != 1 || drops2[0].ID != 204 || pkt2.ID != 205 {
		t.Fatalf("far reentry: drop 204 / return 205, got drops=%v pkt=%d", drops2, pkt2.ID)
	}
	if s := m2.State(); s.Count != 1 || s.NextDrop != 2550 {
		t.Fatalf("expected reset count=1 nextDrop=2550 after >16I, got %+v", s)
	}
}

// 队空时出队返回空、退出丢弃状态并清除首次超标时刻。
func TestEmptyQueueExitsAndClears(t *testing.T) {
	const T, I, maxPkt int64 = 10, 100, 100
	m := mustNew(t, T, I, maxPkt, 100000)

	// 空队列直接出队。
	pkt, dropped, empty := deq(t, m, 0)
	if pkt != nil || !empty || len(dropped) != 0 {
		t.Fatalf("expected empty dequeue, got pkt=%v empty=%v", pkt, empty)
	}

	// 先进入丢弃状态，再把包连续取空：最后一次队空必须退出并清表。
	for id := int64(1); id <= 16; id++ {
		enq(t, m, uint64(id), 100, 0)
	}
	deq(t, m, 200) // 武装
	deq(t, m, 300) // 丢2，进入丢弃状态
	if !m.Dropping() {
		t.Fatalf("expected dropping state after first drop")
	}
	// 在 now=300 反复出队，把队列排空（同刻内不会再丢，因 nextDrop=400）。
	var got []uint64
	for {
		r := m.Dequeue(300)
		if r.Err != nil {
			t.Fatalf("unexpected err: %v", r.Err)
		}
		logOp(t, "Dequeue{now=300} drain", r.Reason)
		if r.Empty {
			break
		}
		got = append(got, r.Packet.ID)
	}
	if m.Dropping() {
		t.Fatalf("expected dropping state cleared after empty")
	}
	if s := m.State(); s.FirstAbove != 0 || s.Count != 0 || s.NextDrop != 0 {
		t.Fatalf("expected all timers cleared on empty, got %+v", s)
	}
	t.Logf("drained (non-dropped) packets: %v", got)
	invariant(t, m)
}
