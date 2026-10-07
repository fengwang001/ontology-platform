package sender

// 本文件实现一个独立的朴素模型，用于与 Scheduler 做随机事件序列
// 对照。模型刻意采用与正式实现不同的结构：缓冲区是逐字节的切片，
// 发送决策逐段循环模拟，并输出每一步的判定依据，便于人工核对日志。

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

type naive struct {
	cfg Config

	buf      []byte // 缓冲中尚未发出的字节（逐字节模拟）
	inFlight int
	sent     int64
	acked    int64
	right    int64

	noDelay bool
	cork    bool
	closed  bool

	corkSince time.Time
	hasCork   bool
	zeroSince time.Time
	hasZero   bool
	lastProbe time.Time
	hasProbe  bool

	lastTime time.Time
	hasTime  bool
}

func newNaive(cfg Config) *naive {
	return &naive{cfg: cfg, right: int64(cfg.InitialWindow)}
}

func (n *naive) checkClock(now time.Time) error {
	if n.hasTime && now.Before(n.lastTime) {
		return ErrClockBackward
	}
	return nil
}

func (n *naive) write(now time.Time, c int) ([]Segment, error, []string) {
	if c <= 0 || c > MaxWriteLen {
		return nil, ErrInvalidParam, nil
	}
	if err := n.checkClock(now); err != nil {
		return nil, err, nil
	}
	if n.closed {
		return nil, ErrWriteAfterClose, nil
	}
	if c > n.cfg.BufferCap-len(n.buf) {
		return nil, ErrBufferFull, nil
	}
	n.lastTime, n.hasTime = now, true
	n.buf = append(n.buf, make([]byte, c)...)
	segs, why := n.transmit(now)
	return segs, nil, why
}

func (n *naive) ack(now time.Time, acked int, window int) ([]Segment, error, []string) {
	if window < 0 {
		return nil, ErrInvalidParam, nil
	}
	if err := n.checkClock(now); err != nil {
		return nil, err, nil
	}
	if n.acked+int64(acked)+int64(window) < n.right {
		return nil, ErrWindowShrink, nil
	}
	if acked < 0 || acked > n.inFlight {
		return nil, ErrAckOutOfRange, nil
	}
	n.lastTime, n.hasTime = now, true
	n.acked += int64(acked)
	n.inFlight -= acked
	n.right = n.acked + int64(window)
	segs, why := n.transmit(now)
	return segs, nil, why
}

func (n *naive) setNoDelay(now time.Time, on bool) ([]Segment, error, []string) {
	if err := n.checkClock(now); err != nil {
		return nil, err, nil
	}
	n.lastTime, n.hasTime = now, true
	n.noDelay = on
	segs, why := n.transmit(now)
	return segs, nil, why
}

func (n *naive) setCork(now time.Time, on bool) ([]Segment, error, []string) {
	if err := n.checkClock(now); err != nil {
		return nil, err, nil
	}
	n.lastTime, n.hasTime = now, true
	n.cork = on
	if !on {
		n.hasCork = false
	}
	segs, why := n.transmit(now)
	return segs, nil, why
}

func (n *naive) advance(now time.Time) ([]Segment, error, []string) {
	if err := n.checkClock(now); err != nil {
		return nil, err, nil
	}
	n.lastTime, n.hasTime = now, true
	segs, why := n.transmit(now)
	return segs, nil, why
}

func (n *naive) close(now time.Time) ([]Segment, error, []string) {
	if err := n.checkClock(now); err != nil {
		return nil, err, nil
	}
	n.lastTime, n.hasTime = now, true
	n.closed = true
	segs, why := n.transmit(now)
	return segs, nil, why
}

// smallAllowed 逐条规则判定，并给出人类可读的判定依据。
func (n *naive) smallAllowed(now time.Time) (bool, string) {
	switch {
	case n.closed:
		return true, "小段放行：已关闭，忽略保留约束"
	case n.cork:
		if !n.hasCork {
			if n.cfg.CorkTimeout <= 0 {
				return true, "小段放行：软木塞超时为零，立即到期"
			}
			return false, fmt.Sprintf("小段保留：软木塞自本时刻起算，超时 %v 未满", n.cfg.CorkTimeout)
		}
		if d := now.Sub(n.corkSince); d >= n.cfg.CorkTimeout {
			return true, fmt.Sprintf("小段放行：软木塞已保留 %v >= 超时 %v", d, n.cfg.CorkTimeout)
		} else {
			return false, fmt.Sprintf("小段保留：软木塞仅 %v < 超时 %v", d, n.cfg.CorkTimeout)
		}
	case n.noDelay:
		return true, "小段放行：不延迟选项打开"
	case n.inFlight == 0:
		return true, "小段放行：默认模式且在途为空"
	default:
		return false, fmt.Sprintf("小段保留：默认模式且在途 %d 字节未确认", n.inFlight)
	}
}

// pending 逐段模拟切分，返回窗口可容纳的满段个数与小段长度。
func (n *naive) pending() (fulls, piece int) {
	usable := n.right - n.sent
	if usable <= 0 || len(n.buf) == 0 {
		return 0, 0
	}
	rest := len(n.buf)
	wnd := usable
	for rest >= n.cfg.MSS && wnd >= int64(n.cfg.MSS) {
		fulls++
		rest -= n.cfg.MSS
		wnd -= int64(n.cfg.MSS)
	}
	p := int64(rest)
	if wnd < p {
		p = wnd
	}
	if p > 0 {
		piece = int(p)
	}
	return fulls, piece
}

// transmit 逐段循环发出数据，并记录每段的判定依据。
func (n *naive) transmit(now time.Time) ([]Segment, []string) {
	var segs []Segment
	var why []string
	for {
		usable := n.right - n.sent
		if usable <= 0 || len(n.buf) == 0 {
			break
		}
		if len(n.buf) >= n.cfg.MSS && usable >= int64(n.cfg.MSS) {
			segs = append(segs, Segment{Len: n.cfg.MSS, At: now})
			n.buf = n.buf[n.cfg.MSS:]
			n.inFlight += n.cfg.MSS
			n.sent += int64(n.cfg.MSS)
			why = append(why, fmt.Sprintf("发满段 %dB：窗口允许，满段总是可发", n.cfg.MSS))
			continue
		}
		piece := len(n.buf)
		if int64(piece) > usable {
			piece = int(usable)
			why = append(why, fmt.Sprintf("窗口仅 %dB，小段被截短", usable))
		}
		if piece == 0 {
			break
		}
		ok, reason := n.smallAllowed(now)
		why = append(why, reason)
		if !ok {
			break
		}
		segs = append(segs, Segment{Len: piece, At: now})
		n.buf = n.buf[piece:]
		n.inFlight += piece
		n.sent += int64(piece)
		why = append(why, fmt.Sprintf("发小段 %dB", piece))
		break
	}
	// 零窗口锚点。
	if n.right-n.sent <= 0 {
		if !n.hasZero {
			n.zeroSince, n.hasZero = now, true
		}
	} else {
		n.hasZero = false
	}
	// 零窗口探测。
	if len(n.buf) > 0 && n.inFlight == 0 && n.right-n.sent <= 0 && n.hasZero {
		base := n.zeroSince
		if n.hasProbe && n.lastProbe.After(base) {
			base = n.lastProbe
		}
		if d := now.Sub(base); d >= n.cfg.ProbeInterval {
			segs = append(segs, Segment{Len: 1, At: now})
			n.buf = n.buf[1:]
			n.inFlight++
			n.sent++
			n.lastProbe, n.hasProbe = now, true
			why = append(why, fmt.Sprintf("零窗口探测 1B：距基点 %v >= 间隔 %v", d, n.cfg.ProbeInterval))
		} else {
			why = append(why, fmt.Sprintf("不探测：距基点 %v < 间隔 %v", d, n.cfg.ProbeInterval))
		}
	}
	// 软木塞锚点。
	if !n.cork || n.closed {
		n.hasCork = false
	} else {
		_, piece := n.pending()
		if !n.hasCork && piece > 0 {
			n.corkSince, n.hasCork = now, true
		}
		if n.hasCork && piece == 0 && n.right-n.sent > 0 {
			n.hasCork = false
		}
	}
	return segs, why
}

func (n *naive) retained() int {
	_, piece := n.pending()
	if piece == 0 {
		return 0
	}
	if ok, _ := n.smallAllowed(n.lastTime); ok {
		return 0
	}
	return piece
}

func (n *naive) nextDeadline() (time.Time, bool) {
	var deadline time.Time
	ok := false
	if n.cork && !n.closed && n.hasCork {
		if d := n.corkSince.Add(n.cfg.CorkTimeout); d.After(n.lastTime) {
			deadline, ok = d, true
		}
	}
	if len(n.buf) > 0 && n.inFlight == 0 && n.right-n.sent <= 0 && n.hasZero {
		base := n.zeroSince
		if n.hasProbe && n.lastProbe.After(base) {
			base = n.lastProbe
		}
		if d := base.Add(n.cfg.ProbeInterval); !ok || d.Before(deadline) {
			deadline, ok = d, true
		}
	}
	return deadline, ok
}

// TestAgainstNaiveModel 用 1000+ 组随机事件序列对照正式实现与朴素
// 模型，逐步打印输入、输出与判定依据（go test -v 可见）。
func TestAgainstNaiveModel(t *testing.T) {
	const sequences = 1200
	for seed := int64(0); seed < sequences; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runSequence(t, seed)
		})
	}
}

func runSequence(t *testing.T, seed int64) {
	r := rand.New(rand.NewSource(seed))
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	cfg := Config{
		MSS:           1 + r.Intn(12),
		InitialWindow: r.Intn(60),
		BufferCap:     20 + r.Intn(200),
		CorkTimeout:   ms(10 * r.Intn(6)),
		ProbeInterval: ms(10 * r.Intn(6)),
	}
	real, err := NewScheduler(cfg)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	model := newNaive(cfg)
	t.Logf("配置: %+v", cfg)

	now := testBase
	steps := 30 + r.Intn(20)
	for step := 0; step < steps; step++ {
		// 推进时钟（偶尔回退以覆盖时钟回退错误）。
		dt := r.Intn(40)
		if r.Intn(20) == 0 {
			dt = -(1 + r.Intn(5))
		}
		now = now.Add(ms(dt))

		var segsR, segsM []Segment
		var errR, errM error
		var why []string
		var desc string
		switch kind := r.Intn(100); {
		case kind < 35:
			n := r.Intn(4*cfg.MSS + 8)
			switch r.Intn(30) {
			case 0:
				n = 0
			case 1:
				n = -1 - r.Intn(5)
			case 2:
				n = MaxWriteLen + 1
			}
			desc = fmt.Sprintf("Write(%d)", n)
			segsR, errR = real.Write(now, n)
			segsM, errM, why = model.write(now, n)
		case kind < 65:
			acked := r.Intn(real.InFlight()+3) - 1
			window := r.Intn(3*cfg.MSS + 2)
			desc = fmt.Sprintf("Ack(%d,%d)", acked, window)
			segsR, errR = real.Ack(now, acked, window)
			segsM, errM, why = model.ack(now, acked, window)
		case kind < 73:
			desc = "Advance()"
			segsR, errR = real.Advance(now)
			segsM, errM, why = model.advance(now)
		case kind < 81:
			on := r.Intn(2) == 0
			desc = fmt.Sprintf("SetNoDelay(%v)", on)
			segsR, errR = real.SetNoDelay(now, on)
			segsM, errM, why = model.setNoDelay(now, on)
		case kind < 89:
			on := r.Intn(2) == 0
			desc = fmt.Sprintf("SetCork(%v)", on)
			segsR, errR = real.SetCork(now, on)
			segsM, errM, why = model.setCork(now, on)
		default:
			desc = "Close()"
			segsR, errR = real.Close(now)
			segsM, errM, why = model.close(now)
		}

		dlR, okR := real.NextDeadline()
		dlM, okM := model.nextDeadline()
		t.Logf("step %02d t=%+v %s -> segs=%v err=%v | 在途=%d 缓冲=%d 保留=%d 期限=%v,%v | 依据: %v",
			step, now.Sub(testBase), desc, segsR, errR,
			real.InFlight(), real.Buffered(), real.Retained(), dlR, okR, why)

		if errR != errM {
			t.Fatalf("step %d %s: err = %v, model = %v", step, desc, errR, errM)
		}
		if !reflect.DeepEqual(segsR, segsM) {
			t.Fatalf("step %d %s: segs = %v, model = %v", step, desc, segsR, segsM)
		}
		if real.InFlight() != model.inFlight {
			t.Fatalf("step %d %s: InFlight = %d, model = %d", step, desc, real.InFlight(), model.inFlight)
		}
		if real.Buffered() != len(model.buf) {
			t.Fatalf("step %d %s: Buffered = %d, model = %d", step, desc, real.Buffered(), len(model.buf))
		}
		if real.Retained() != model.retained() {
			t.Fatalf("step %d %s: Retained = %d, model = %d", step, desc, real.Retained(), model.retained())
		}
		if okR != okM || (okR && !dlR.Equal(dlM)) {
			t.Fatalf("step %d %s: NextDeadline = %v,%v, model = %v,%v", step, desc, dlR, okR, dlM, okM)
		}
	}
}
