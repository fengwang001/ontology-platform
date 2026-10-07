package txsched

import (
	"math"
	"sync"
	"testing"
)

func mustNew(t *testing.T, cfg Config) *Scheduler {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%+v) failed: %v", cfg, err)
	}
	return s
}

func write(tm int64, n int) Event { return Event{Time: tm, Kind: KindWrite, Bytes: n} }

func ack(tm int64, ackNum, wnd int) Event {
	return Event{Time: tm, Kind: KindAck, Bytes: ackNum, Window: wnd}
}

func noDelay(tm int64, on bool) Event { return Event{Time: tm, Kind: KindNoDelay, On: on} }

func cork(tm int64, on bool) Event { return Event{Time: tm, Kind: KindCork, On: on} }

func tick(tm int64) Event { return Event{Time: tm, Kind: KindTime} }

func closeEv(tm int64) Event { return Event{Time: tm, Kind: KindClose} }

// check 处理事件并断言无错误、输出段与期望一致。
func check(t *testing.T, s *Scheduler, ev Event, want ...Segment) {
	t.Helper()
	out := s.Do(ev)
	if out.Err != nil {
		t.Fatalf("Do(%+v) unexpected error: %v", ev, out.Err)
	}
	if !segsEqual(out.Segments, want) {
		t.Fatalf("Do(%+v) segments = %v, want %v", ev, out.Segments, want)
	}
}

func checkErr(t *testing.T, s *Scheduler, ev Event, wantErr error) {
	t.Helper()
	before := s.Snapshot()
	out := s.Do(ev)
	if out.Err != wantErr {
		t.Fatalf("Do(%+v) err = %v, want %v", ev, out.Err, wantErr)
	}
	if len(out.Segments) != 0 {
		t.Fatalf("Do(%+v) rejected but emitted %v", ev, out.Segments)
	}
	if after := s.Snapshot(); after != before {
		t.Fatalf("rejected Do(%+v) mutated state: %+v -> %+v", ev, before, after)
	}
}

func segsEqual(a, b []Segment) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func seg(n int, tm int64) Segment { return Segment{Len: n, Time: tm} }

func probe(tm int64) Segment { return Segment{Len: 1, Time: tm, Probe: true} }

func TestConfigValidation(t *testing.T) {
	base := Config{MSS: 10, BufferMax: 100, InitialWindow: 100, CorkTimeout: 50, ProbeInterval: 25}
	if _, err := New(base); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	bad := []Config{
		{MSS: 0, BufferMax: 100},
		{MSS: -1, BufferMax: 100},
		{MSS: 10, BufferMax: -1},
		{MSS: 10, BufferMax: 100, InitialWindow: -1},
		{MSS: 10, BufferMax: 100, CorkTimeout: -1},
		{MSS: 10, BufferMax: 100, ProbeInterval: -1},
	}
	for i, cfg := range bad {
		if _, err := New(cfg); err != ErrInvalidArg {
			t.Fatalf("bad config %d: err = %v, want ErrInvalidArg", i, err)
		}
	}
}

// 满段与小段边界：恰好 M、M-1、M+1。
func TestFullAndSmallBoundary(t *testing.T) {
	cfg := Config{MSS: 10, BufferMax: 1000, InitialWindow: 1000, CorkTimeout: 50, ProbeInterval: 25}
	s := mustNew(t, cfg)

	// 恰好一个满段：立即发出。
	check(t, s, write(0, 10), seg(10, 0))
	// M-1：在途非空，保留。
	check(t, s, write(1, 9))
	if snap := s.Snapshot(); snap.Buffered != 9 || snap.Reserved != 9 {
		t.Fatalf("held small segment: %+v", snap)
	}
	// 再写 1 字节凑满满段，立即发出。
	check(t, s, write(2, 1), seg(10, 2))
	// 在途清空后，M-1 的小段可立即发出。
	check(t, s, ack(3, 20, 1000))
	check(t, s, write(4, 9), seg(9, 4))
	if snap := s.Snapshot(); snap.InFlight != 9 || snap.Reserved != 0 {
		t.Fatalf("small segment sent while idle: %+v", snap)
	}
}

// 默认模式（Nagle）：在途非空时小段保留，全部确认后释放。
func TestNagleHoldAndRelease(t *testing.T) {
	cfg := Config{MSS: 10, BufferMax: 1000, InitialWindow: 1000, CorkTimeout: 50, ProbeInterval: 25}
	s := mustNew(t, cfg)

	check(t, s, write(0, 25), seg(10, 0), seg(10, 0))
	if snap := s.Snapshot(); snap.Reserved != 5 || snap.Buffered != 5 {
		t.Fatalf("tail held: %+v", snap)
	}
	// 部分确认：在途仍非空，继续保留。
	check(t, s, ack(1, 10, 1000))
	if snap := s.Snapshot(); snap.Reserved != 5 {
		t.Fatalf("partial ack keeps hold: %+v", snap)
	}
	// 全部确认：释放。
	check(t, s, ack(2, 20, 1000), seg(5, 2))
	if snap := s.Snapshot(); snap.Reserved != 0 || snap.Buffered != 0 {
		t.Fatalf("released after full ack: %+v", snap)
	}
}

// 窗口恰好等于小段长度与差一字节。
func TestWindowExactAndOneShort(t *testing.T) {
	// 窗口恰好等于小段长度：整段发出。
	cfg := Config{MSS: 10, BufferMax: 1000, InitialWindow: 7, CorkTimeout: 50, ProbeInterval: 25}
	s := mustNew(t, cfg)
	check(t, s, noDelay(0, true))
	check(t, s, write(1, 7), seg(7, 1))

	// 窗口差一字节：只能发出窗口允许的部分，余量留缓冲。
	cfg.InitialWindow = 6
	s = mustNew(t, cfg)
	check(t, s, noDelay(0, true))
	check(t, s, write(1, 7), seg(6, 1))
	if snap := s.Snapshot(); snap.Buffered != 1 || snap.Reserved != 0 {
		t.Fatalf("truncated by window: %+v", snap)
	}
	check(t, s, ack(2, 6, 1), seg(1, 2))
	if snap := s.Snapshot(); snap.Buffered != 0 {
		t.Fatalf("remainder sent after window opens: %+v", snap)
	}
}

// 被窗口截短的段不再视为满段。
func TestWindowTruncatedIsNotFull(t *testing.T) {
	cfg := Config{MSS: 10, BufferMax: 1000, InitialWindow: 7, CorkTimeout: 50, ProbeInterval: 25}
	s := mustNew(t, cfg)
	// 默认模式、在途为空：截短的 7 字节可发，但它不是满段。
	check(t, s, write(0, 17), seg(7, 0))
	if snap := s.Snapshot(); snap.Buffered != 10 {
		t.Fatalf("rest waits for window: %+v", snap)
	}
	// 窗口打开后，缓冲中的 10 字节重新凑成一个满段发出。
	check(t, s, ack(1, 7, 10), seg(10, 1))
}

// 软木塞超时恰在边界：t = 进入时刻 + 超时 时释放。
func TestCorkTimeoutBoundary(t *testing.T) {
	cfg := Config{MSS: 10, BufferMax: 1000, InitialWindow: 1000, CorkTimeout: 50, ProbeInterval: 25}
	s := mustNew(t, cfg)

	check(t, s, cork(0, true))
	check(t, s, write(10, 3))
	if snap := s.Snapshot(); !snap.HasTimer || snap.NextTimer != 60 {
		t.Fatalf("cork timer armed at 60: %+v", snap)
	}
	check(t, s, tick(59))             // 边界前：仍保留
	check(t, s, tick(60), seg(3, 60)) // 恰在边界：释放

	// 满段照发，仅尾部保留；关闭软木塞立即解除。
	check(t, s, write(70, 13), seg(10, 70))
	if snap := s.Snapshot(); snap.Reserved != 3 || snap.NextTimer != 120 {
		t.Fatalf("cork holds tail: %+v", snap)
	}
	// 关闭软木塞解除软木塞保留，但默认模式的 Nagle 保留仍适用（在途非空）。
	check(t, s, cork(80, false))
	if snap := s.Snapshot(); snap.Reserved != 3 {
		t.Fatalf("nagle still holds after cork off: %+v", snap)
	}
	check(t, s, ack(90, 13, 1000), seg(3, 90))
}

// 两个选项同时打开时，软木塞优先。
func TestBothOptionsCorkWins(t *testing.T) {
	cfg := Config{MSS: 10, BufferMax: 1000, InitialWindow: 1000, CorkTimeout: 50, ProbeInterval: 25}
	s := mustNew(t, cfg)

	check(t, s, noDelay(0, true))
	check(t, s, cork(1, true))
	// 不延迟打开但在途为空：软木塞仍然保留。
	check(t, s, write(2, 3))
	if snap := s.Snapshot(); snap.Reserved != 3 {
		t.Fatalf("cork overrides no-delay: %+v", snap)
	}
	// 超时释放。
	check(t, s, tick(52), seg(3, 52))
	// 再次保留后，关闭软木塞：不延迟生效，立即发出。
	check(t, s, write(53, 4))
	check(t, s, cork(54, false), seg(4, 54))
}

// 关闭后残余数据在窗口受限时分批发出，直至清空。
func TestCloseDrainsInBatches(t *testing.T) {
	cfg := Config{MSS: 10, BufferMax: 1000, InitialWindow: 12, CorkTimeout: 50, ProbeInterval: 25}
	s := mustNew(t, cfg)

	check(t, s, write(0, 25), seg(10, 0))
	if snap := s.Snapshot(); snap.Reserved != 15 {
		t.Fatalf("tail held before close: %+v", snap)
	}
	// 关闭：忽略保留约束，但窗口只剩 2，先发 2。
	check(t, s, closeEv(1), seg(2, 1))
	if snap := s.Snapshot(); snap.Buffered != 13 {
		t.Fatalf("window-limited after close: %+v", snap)
	}
	// 后续确认与通告驱动剩余数据分批发出。
	check(t, s, ack(2, 12, 10), seg(10, 2))
	check(t, s, ack(3, 22, 5), seg(3, 3))
	if snap := s.Snapshot(); snap.Buffered != 0 {
		t.Fatalf("drained: %+v", snap)
	}
	checkErr(t, s, write(4, 1), ErrClosed)
}

// 零窗口探测：开始、边界触发、探测计入在途、确认后重新计时。
func TestZeroWindowProbe(t *testing.T) {
	cfg := Config{MSS: 10, BufferMax: 1000, InitialWindow: 5, CorkTimeout: 50, ProbeInterval: 25}
	s := mustNew(t, cfg)

	check(t, s, write(0, 5), seg(5, 0))
	check(t, s, ack(1, 5, 0)) // 在途清空，窗口为零
	check(t, s, write(2, 7))  // 窗口为零，缓冲非空，在途为空
	if snap := s.Snapshot(); !snap.HasTimer || snap.NextTimer != 27 {
		t.Fatalf("probe armed at 27: %+v", snap)
	}
	check(t, s, tick(26))            // 边界前：不探测
	check(t, s, tick(27), probe(27)) // 恰在边界：探测 1 字节
	if snap := s.Snapshot(); snap.InFlight != 1 || snap.Buffered != 6 || snap.HasTimer {
		t.Fatalf("probe counts as in-flight: %+v", snap)
	}
	// 探测字节被确认：自上次探测起满间隔再探。
	check(t, s, ack(30, 6, 0))
	if snap := s.Snapshot(); !snap.HasTimer || snap.NextTimer != 52 {
		t.Fatalf("probe re-armed at 52: %+v", snap)
	}
	check(t, s, tick(51))
	check(t, s, tick(52), probe(52))
	// 窗口打开：其余数据只取决于最新窗口。
	check(t, s, ack(60, 7, 10), seg(5, 60))
	if snap := s.Snapshot(); snap.Buffered != 0 || snap.HasTimer {
		t.Fatalf("window open, drained: %+v", snap)
	}
}

// 零窗口探测的取消：窗口在探测到期前打开。
func TestZeroWindowProbeCancel(t *testing.T) {
	cfg := Config{MSS: 10, BufferMax: 1000, InitialWindow: 5, CorkTimeout: 50, ProbeInterval: 25}
	s := mustNew(t, cfg)

	check(t, s, write(0, 5), seg(5, 0))
	check(t, s, ack(1, 5, 0))
	check(t, s, write(2, 7))
	if snap := s.Snapshot(); !snap.HasTimer || snap.NextTimer != 27 {
		t.Fatalf("probe armed: %+v", snap)
	}
	// 窗口打开，数据发出，探测取消。
	check(t, s, ack(3, 5, 10), seg(7, 3))
	if snap := s.Snapshot(); snap.HasTimer {
		t.Fatalf("probe cancelled: %+v", snap)
	}
	check(t, s, tick(27))
	check(t, s, tick(100))
}

// 在途非空时不探测。
func TestNoProbeWhileInFlight(t *testing.T) {
	cfg := Config{MSS: 10, BufferMax: 1000, InitialWindow: 5, CorkTimeout: 50, ProbeInterval: 25}
	s := mustNew(t, cfg)

	check(t, s, write(0, 5), seg(5, 0))
	check(t, s, write(1, 7))
	// 在途非空（5 字节未确认）：即使过了探测间隔也不探测。
	check(t, s, tick(100))
	if snap := s.Snapshot(); snap.InFlight != 5 || snap.Buffered != 7 {
		t.Fatalf("no probe while in-flight: %+v", snap)
	}
}

// 错误可区分、固定优先级，且被拒绝的事件不改变任何状态（含时钟）。
func TestRejectionOrder(t *testing.T) {
	cfg := Config{MSS: 10, BufferMax: 10, InitialWindow: 0, CorkTimeout: 50, ProbeInterval: 25}
	s := mustNew(t, cfg)

	// 参数非法：零长度、负长度、超过 2^31-1。
	checkErr(t, s, write(0, 0), ErrInvalidArg)
	checkErr(t, s, write(0, -5), ErrInvalidArg)
	checkErr(t, s, write(0, math.MaxInt32+1), ErrInvalidArg)
	// 2^31-1 本身合法，但超出缓冲上限：报缓冲已满而非参数非法。
	checkErr(t, s, write(0, math.MaxInt32), ErrBufferFull)

	// 写满缓冲；整次拒绝，不接受部分。
	check(t, s, write(0, 10))
	checkErr(t, s, write(1, 1), ErrBufferFull)
	if snap := s.Snapshot(); snap.Buffered != 10 {
		t.Fatalf("no partial accept: %+v", snap)
	}

	// 打开窗口，数据发出，右边缘推进到 20。
	check(t, s, ack(1, 0, 20), seg(10, 1))
	// 窗口收缩：新右边缘 15 < 20。
	checkErr(t, s, ack(2, 5, 10), ErrWindowShrink)
	// 收缩优先于确认越界：ack 15 既收缩（15<20）又越界（15>在途10）。
	checkErr(t, s, ack(2, 15, 0), ErrWindowShrink)
	// 确认越界：确认字节超过在途。
	checkErr(t, s, ack(2, 11, 20), ErrAckRange)
	// 合法确认。
	check(t, s, ack(2, 9, 20))
	// 确认越界：确认字节为负（累计确认倒退）。
	checkErr(t, s, ack(2, 8, 21), ErrAckRange)

	// 时钟回退；参数非法优先于时钟回退。
	check(t, s, tick(3))
	checkErr(t, s, tick(2), ErrClockRollback)
	checkErr(t, s, write(2, 0), ErrInvalidArg)
	// 被拒绝的事件不推进时钟：t=20 的非法事件被拒后，t=4 仍可接受。
	checkErr(t, s, write(20, 0), ErrInvalidArg)
	check(t, s, write(4, 5))
	if snap := s.Snapshot(); snap.Reserved != 5 {
		t.Fatalf("clock untouched by rejected event: %+v", snap)
	}
}

// 关闭后写入优先于缓冲已满。
func TestClosedBeforeBufferFull(t *testing.T) {
	cfg := Config{MSS: 10, BufferMax: 10, InitialWindow: 0, CorkTimeout: 50, ProbeInterval: 25}
	s := mustNew(t, cfg)
	check(t, s, write(0, 10))
	check(t, s, closeEv(1))
	// 缓冲已满（10+1>10）且已关闭：报关闭后写入。
	checkErr(t, s, write(2, 1), ErrClosed)
	// 关闭后不再接受写入，但确认、时间推进仍接受。
	check(t, s, tick(3))
}

// 状态查询不改变任何状态。
func TestSnapshotNoMutation(t *testing.T) {
	cfg := Config{MSS: 10, BufferMax: 1000, InitialWindow: 100, CorkTimeout: 50, ProbeInterval: 25}
	s := mustNew(t, cfg)
	check(t, s, cork(0, true))
	check(t, s, write(1, 3))

	snap1 := s.Snapshot()
	snap2 := s.Snapshot()
	if snap1 != snap2 {
		t.Fatalf("snapshot mutated state: %+v vs %+v", snap1, snap2)
	}
	stats1 := s.Stats()
	// 查询之后，软木塞仍在 t=51 准时释放，如同查询从未发生。
	check(t, s, tick(51), seg(3, 51))
	if stats1.Accepted != 2 {
		t.Fatalf("stats: %+v", stats1)
	}
}

// 均摊复杂度可验证证明：块队列操作次数随事件数线性增长，
// 与缓冲数据总量和历史无关。
func TestAmortizedCost(t *testing.T) {
	const writes = 20000
	cfg := Config{MSS: 4, BufferMax: 1 << 20, InitialWindow: 0, CorkTimeout: 5, ProbeInterval: 5}
	s := mustNew(t, cfg)

	for i := 0; i < writes; i++ {
		// 零窗口下 t=5 会触发一次 1 字节探测，属预期输出。
		if out := s.Do(write(int64(i), 3)); out.Err != nil {
			t.Fatalf("write %d failed: %v", i, out.Err)
		}
	}
	// 确认探测字节并打开窗口：一次事件发出全部积压数据。
	out := s.Do(ack(writes, 1, 1<<20))
	if out.Err != nil {
		t.Fatalf("ack failed: %v", out.Err)
	}
	st := s.Stats()
	t.Logf("stats after %d writes: %+v", writes, st)
	if st.ChunkPushes > writes {
		t.Fatalf("pushes %d exceed writes %d", st.ChunkPushes, writes)
	}
	if st.ChunkPops > st.ChunkPushes {
		t.Fatalf("pops %d exceed pushes %d", st.ChunkPops, st.ChunkPushes)
	}
	if st.CompactMoves > st.ChunkPops {
		t.Fatalf("compaction moves %d exceed pops %d", st.CompactMoves, st.ChunkPops)
	}
	if st.Accepted != writes+1 {
		t.Fatalf("accepted %d, want %d", st.Accepted, writes+1)
	}
	// 每个块至多入队一次、出队一次、被搬移一次：总开销 O(事件数)。
	if st.ChunkPushes+st.ChunkPops+st.CompactMoves > 3*writes {
		t.Fatalf("chunk ops not linear in events: %+v", st)
	}
}

// 并发调用等价于某个串行顺序（互斥锁保证可线性化），
// 在 -race 下运行以检测数据竞争。
func TestConcurrentLinearizable(t *testing.T) {
	cfg := Config{MSS: 4, BufferMax: 1 << 20, InitialWindow: 1 << 20, CorkTimeout: 10, ProbeInterval: 10}
	s := mustNew(t, cfg)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				tm := int64(i)
				switch (g + i) % 4 {
				case 0:
					s.Do(write(tm, 1+g))
				case 1:
					snap := s.Snapshot()
					s.Do(ack(tm, snap.Acked+snap.InFlight, 1<<20))
				case 2:
					s.Do(tick(tm))
				case 3:
					s.Snapshot()
				}
			}
		}(g)
	}
	wg.Wait()

	snap := s.Snapshot()
	if snap.InFlight < 0 || snap.Buffered < 0 || snap.Reserved < 0 {
		t.Fatalf("invariants broken: %+v", snap)
	}
	if snap.Reserved != 0 && snap.Reserved != snap.Buffered {
		t.Fatalf("reserved invariant broken: %+v", snap)
	}
	if snap.Sent < snap.Acked {
		t.Fatalf("sent < acked: %+v", snap)
	}
}
