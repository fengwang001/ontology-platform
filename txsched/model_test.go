package txsched

// 本文件包含一个独立实现的朴素模型，以及它与真实实现之间的随机差分测试。
//
// 朴素模型直接按题面规则书写，刻意采用低效的表示（缓冲为记录切片、
// 每次重新求和、保留量按需重算），与真实实现的块队列、增量维护无关，
// 从而能交叉验证真实实现的正确性。

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"strconv"
	"testing"
)

// nrec 为朴素模型中一次写入的记录。
type nrec struct {
	tm int64
	n  int
}

// naive 为朴素模型：直接按规则逐步模拟，不考虑效率。
type naive struct {
	cfg Config

	now    int64
	hasNow bool

	closed  bool
	noDelay bool
	cork    bool

	sent      int
	acked     int
	rightEdge int

	buf []nrec

	zeroSet   bool
	zeroSince int64
	probed    bool
	lastProbe int64
}

func newNaive(cfg Config) *naive {
	return &naive{cfg: cfg, rightEdge: cfg.InitialWindow}
}

func (n *naive) buffered() int {
	total := 0
	for _, r := range n.buf {
		total += r.n
	}
	return total
}

func (n *naive) inFlight() int { return n.sent - n.acked }

func (n *naive) front() int64 { return n.buf[0].tm }

func (n *naive) pop(k int) {
	for k > 0 {
		if n.buf[0].n <= k {
			k -= n.buf[0].n
			n.buf = n.buf[1:]
		} else {
			n.buf[0].n -= k
			k = 0
		}
	}
}

// hold 报告当前队首小段是否应被保留约束扣留。
func (n *naive) hold(segLen int) bool {
	if segLen >= n.cfg.MSS || n.closed {
		return false
	}
	if n.cork {
		return n.now < n.front()+n.cfg.CorkTimeout
	}
	return !n.noDelay && n.inFlight() > 0
}

func (n *naive) send() []Segment {
	var segs []Segment
	for n.buffered() > 0 {
		wnd := n.rightEdge - n.sent
		if wnd <= 0 {
			break
		}
		segLen := min(n.cfg.MSS, n.buffered(), wnd)
		if n.hold(segLen) {
			break
		}
		segs = append(segs, Segment{Len: segLen, Time: n.now})
		n.pop(segLen)
		n.sent += segLen
	}
	n.trackZero()
	if n.zeroSet && n.inFlight() == 0 {
		base := n.zeroSince
		if n.probed && n.lastProbe > base {
			base = n.lastProbe
		}
		if n.now >= base+n.cfg.ProbeInterval {
			segs = append(segs, Segment{Len: 1, Time: n.now, Probe: true})
			n.pop(1)
			n.sent++
			n.lastProbe = n.now
			n.probed = true
			n.trackZero()
		}
	}
	return segs
}

func (n *naive) trackZero() {
	if n.rightEdge-n.sent <= 0 && n.buffered() > 0 {
		if !n.zeroSet {
			n.zeroSet = true
			n.zeroSince = n.now
		}
	} else {
		n.zeroSet = false
	}
}

// reserved 按需重算被保留的小段字节数：
// 缓冲非空、窗口有余量、队首不足一个满段且被保留约束扣留时，
// 全部缓冲都处于被保留状态。
func (n *naive) reserved() int {
	b := n.buffered()
	if b == 0 {
		return 0
	}
	wnd := n.rightEdge - n.sent
	if wnd <= 0 {
		return 0
	}
	segLen := min(n.cfg.MSS, b, wnd)
	if n.hold(segLen) {
		return b
	}
	return 0
}

func (n *naive) do(ev Event) Outcome {
	switch ev.Kind {
	case KindWrite:
		if ev.Bytes <= 0 || ev.Bytes > math.MaxInt32 {
			return Outcome{Err: ErrInvalidArg}
		}
	case KindAck:
		if ev.Window < 0 {
			return Outcome{Err: ErrInvalidArg}
		}
	case KindNoDelay, KindCork, KindTime, KindClose:
	default:
		return Outcome{Err: ErrInvalidArg}
	}
	if n.hasNow && ev.Time < n.now {
		return Outcome{Err: ErrClockRollback}
	}
	switch ev.Kind {
	case KindWrite:
		if n.closed {
			return Outcome{Err: ErrClosed}
		}
		if n.buffered()+ev.Bytes > n.cfg.BufferMax {
			return Outcome{Err: ErrBufferFull}
		}
	case KindAck:
		if ev.Bytes < n.rightEdge-ev.Window {
			return Outcome{Err: ErrWindowShrink}
		}
		if ev.Bytes < n.acked || ev.Bytes-n.acked > n.inFlight() {
			return Outcome{Err: ErrAckRange}
		}
	}
	n.now = ev.Time
	n.hasNow = true
	switch ev.Kind {
	case KindWrite:
		n.buf = append(n.buf, nrec{tm: ev.Time, n: ev.Bytes})
	case KindAck:
		n.acked = ev.Bytes
		n.rightEdge = ev.Bytes + ev.Window
	case KindNoDelay:
		n.noDelay = ev.On
	case KindCork:
		n.cork = ev.On
	case KindClose:
		n.closed = true
	case KindTime:
	}
	return Outcome{Segments: n.send()}
}

func (n *naive) snapshot() Snapshot {
	snap := Snapshot{
		InFlight: n.inFlight(),
		Buffered: n.buffered(),
		Reserved: n.reserved(),
		Sent:     n.sent,
		Acked:    n.acked,
		Closed:   n.closed,
		NoDelay:  n.noDelay,
		Cork:     n.cork,
	}
	var best int64
	ok := false
	if n.cork && snap.Reserved > 0 {
		best = n.front() + n.cfg.CorkTimeout
		ok = true
	}
	if n.zeroSet && n.inFlight() == 0 {
		base := n.zeroSince
		if n.probed && n.lastProbe > base {
			base = n.lastProbe
		}
		if t := base + n.cfg.ProbeInterval; !ok || t < best {
			best = t
			ok = true
		}
	}
	snap.NextTimer = best
	snap.HasTimer = ok
	return snap
}

// genEvent 依据当前快照生成一个随机事件（合法与非法混杂）。
func genEvent(rng *rand.Rand, cfg Config, snap Snapshot, now *int64) Event {
	// 时钟：多数前进，偶尔回退以覆盖时钟回退分支。
	if rng.Intn(100) < 85 {
		*now += int64(rng.Intn(6))
	} else {
		*now -= int64(rng.Intn(8))
	}
	tm := *now
	switch r := rng.Intn(100); {
	case r < 32:
		n := rng.Intn(3*cfg.MSS + 3)
		switch rng.Intn(100) {
		case 0:
			n = 0 // 非法：零长度
		case 1:
			n = math.MaxInt32 + rng.Intn(2) // 非法：超过 2^31-1
		case 2:
			n = -rng.Intn(10) // 非法：负长度
		}
		return Event{Time: tm, Kind: KindWrite, Bytes: n}
	case r < 58:
		// 围绕合法范围生成累计确认与窗口通告。
		ackNum := snap.Acked + rng.Intn(snap.InFlight+3) - 1
		wnd := rng.Intn(2*cfg.MSS + 3)
		switch rng.Intn(100) {
		case 0:
			wnd = -1 // 非法：负窗口
		case 1:
			ackNum = snap.Acked + snap.InFlight + 1 + rng.Intn(3) // 越界：超过在途
		case 2:
			ackNum = snap.Acked - 1 - rng.Intn(3) // 越界：确认倒退
		}
		return Event{Time: tm, Kind: KindAck, Bytes: ackNum, Window: wnd}
	case r < 68:
		return Event{Time: tm, Kind: KindNoDelay, On: rng.Intn(2) == 0}
	case r < 78:
		return Event{Time: tm, Kind: KindCork, On: rng.Intn(2) == 0}
	case r < 93:
		return Event{Time: tm, Kind: KindTime}
	case r < 97:
		return Event{Time: tm, Kind: KindClose}
	default:
		return Event{Time: tm, Kind: Kind(rng.Intn(8))} // 可能为非法种类
	}
}

// TestDifferentialRandom 将真实实现与朴素模型对照：
// 1500 组随机事件序列，逐步比对错误、输出段与状态快照，
// 并打印每步的输入、输出与判定依据。
func TestDifferentialRandom(t *testing.T) {
	seed := int64(1581)
	if s := os.Getenv("TXSCHED_SEED"); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			t.Fatalf("bad TXSCHED_SEED %q: %v", s, err)
		}
		seed = v
	}
	rng := rand.New(rand.NewSource(seed))
	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		cfg := Config{
			MSS:           1 + rng.Intn(8),
			BufferMax:     rng.Intn(61),
			InitialWindow: rng.Intn(31),
			CorkTimeout:   int64(rng.Intn(21)),
			ProbeInterval: int64(rng.Intn(21)),
		}
		real, err := New(cfg)
		if err != nil {
			t.Fatalf("seq=%d New(%+v): %v", seq, cfg, err)
		}
		model := newNaive(cfg)
		var now int64
		steps := 20 + rng.Intn(41)
		for step := 0; step < steps; step++ {
			ev := genEvent(rng, cfg, real.Snapshot(), &now)
			before := real.Snapshot()
			got := real.Do(ev)
			want := model.do(ev)
			after := real.Snapshot()
			t.Logf("seq=%d step=%d cfg=%+v ev=%+v => segs=%v err=%v snap=%+v",
				seq, step, cfg, ev, got.Segments, got.Err, after)
			if got.Err != want.Err {
				t.Fatalf("seq=%d step=%d ev=%+v: err = %v, model = %v",
					seq, step, ev, got.Err, want.Err)
			}
			if !segsEqual(got.Segments, want.Segments) {
				t.Fatalf("seq=%d step=%d ev=%+v: segs = %v, model = %v",
					seq, step, ev, got.Segments, want.Segments)
			}
			if wantSnap := model.snapshot(); after != wantSnap {
				t.Fatalf("seq=%d step=%d ev=%+v: snap = %+v, model = %+v",
					seq, step, ev, after, wantSnap)
			}
			if got.Err != nil && after != before {
				t.Fatalf("seq=%d step=%d ev=%+v: rejected event mutated state %+v -> %+v",
					seq, step, ev, before, after)
			}
			if after.Reserved != 0 && after.Reserved != after.Buffered {
				t.Fatalf("seq=%d step=%d: reserved invariant broken: %+v", seq, step, after)
			}
			if after.InFlight < 0 || after.Buffered < 0 || after.Sent < after.Acked {
				t.Fatalf("seq=%d step=%d: counter invariant broken: %+v", seq, step, after)
			}
		}
	}
}

// TestDifferentialLogExample 打印一条完整序列的逐步日志样例，
// 便于人工核对判定依据（go test -run TestDifferentialLogExample -v）。
func TestDifferentialLogExample(t *testing.T) {
	cfg := Config{MSS: 4, BufferMax: 20, InitialWindow: 6, CorkTimeout: 10, ProbeInterval: 5}
	s := mustNew(t, cfg)
	model := newNaive(cfg)
	events := []Event{
		write(0, 9),       // 满段发出，尾部被 Nagle 保留
		cork(1, true),     // 软木塞接管保留，计时器=10
		tick(9),           // 超时边界前：仍保留
		tick(10),          // 恰在边界释放，窗口截短发 2
		write(11, 3),      // 零窗口，缓冲积压
		ack(12, 6, 0),     // 在途清空，探测计时器=15
		tick(14),          // 探测边界前：不探测
		tick(15),          // 恰在边界：探测 1 字节
		ack(20, 7, 4),     // 窗口打开，发出满段
		ack(25, 11, 0),    // 在途清空且已过探测时刻：立即探测
		noDelay(26, true), // 打开不延迟
		write(27, 7),      // 零窗口，缓冲积压
		ack(28, 12, 10),   // 满段发出；软木塞优先于不延迟，尾部保留
		tick(36),          // 软木塞边界前：仍保留
		tick(37),          // 恰在边界释放
		write(38, 5),      // 窗口只剩 3，软木塞保留
		closeEv(39),       // 关闭：忽略保留，按窗口发 3
		ack(40, 19, 3),    // 右边缘不变，合法确认
		ack(41, 22, 0),    // 在途清空，零窗口（自 t=39 起），探测计时器=44
		tick(45),          // 已过 44：关闭后探测仍然生效
		ack(50, 23, 4),    // 窗口打开，残余数据发完
		write(51, 1),      // 关闭后写入：拒绝
	}
	for i, ev := range events {
		got := s.Do(ev)
		want := model.do(ev)
		snap := s.Snapshot()
		t.Logf("step=%d ev=%s => segs=%v err=%v | inflight=%d buffered=%d reserved=%d timer=(%d,%v)",
			i, formatEvent(ev), got.Segments, got.Err,
			snap.InFlight, snap.Buffered, snap.Reserved, snap.NextTimer, snap.HasTimer)
		if got.Err != want.Err || !segsEqual(got.Segments, want.Segments) || snap != model.snapshot() {
			t.Fatalf("step=%d ev=%+v: got (%v,%v,%+v), model (%v,%v,%+v)",
				i, ev, got.Segments, got.Err, snap, want.Segments, want.Err, model.snapshot())
		}
	}
}

func formatEvent(ev Event) string {
	switch ev.Kind {
	case KindWrite:
		return fmt.Sprintf("write(%d)@%d", ev.Bytes, ev.Time)
	case KindAck:
		return fmt.Sprintf("ack(%d,wnd=%d)@%d", ev.Bytes, ev.Window, ev.Time)
	case KindNoDelay:
		return fmt.Sprintf("nodelay(%v)@%d", ev.On, ev.Time)
	case KindCork:
		return fmt.Sprintf("cork(%v)@%d", ev.On, ev.Time)
	case KindTime:
		return fmt.Sprintf("tick@%d", ev.Time)
	case KindClose:
		return fmt.Sprintf("close@%d", ev.Time)
	default:
		return fmt.Sprintf("kind(%d)@%d", int(ev.Kind), ev.Time)
	}
}
