package sender

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

var testBase = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(ms int) time.Time { return testBase.Add(time.Duration(ms) * time.Millisecond) }

func newTest(t *testing.T, cfg Config) *Scheduler {
	t.Helper()
	s, err := NewScheduler(cfg)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	return s
}

// checkSegs 断言事件无错误且发出的段长序列与 want 一致。
func checkSegs(t *testing.T, got []Segment, err error, want ...int) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var lens []int
	for _, seg := range got {
		lens = append(lens, seg.Len)
	}
	if !reflect.DeepEqual(lens, want) {
		t.Fatalf("segments = %v, want %v", lens, want)
	}
}

func checkErr(t *testing.T, err error, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func checkState(t *testing.T, s *Scheduler, inFlight, buffered, retained int) {
	t.Helper()
	if got := s.InFlight(); got != inFlight {
		t.Fatalf("InFlight = %d, want %d", got, inFlight)
	}
	if got := s.Buffered(); got != buffered {
		t.Fatalf("Buffered = %d, want %d", got, buffered)
	}
	if got := s.Retained(); got != retained {
		t.Fatalf("Retained = %d, want %d", got, retained)
	}
}

func TestConfigValidation(t *testing.T) {
	valid := Config{MSS: 10, InitialWindow: 0, BufferCap: 100}
	if _, err := NewScheduler(valid); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	bads := []Config{
		{MSS: 0, InitialWindow: 0, BufferCap: 100},
		{MSS: -1, InitialWindow: 0, BufferCap: 100},
		{MSS: 10, InitialWindow: -1, BufferCap: 100},
		{MSS: 10, InitialWindow: 0, BufferCap: -1},
		{MSS: 10, InitialWindow: 0, BufferCap: 100, CorkTimeout: -time.Millisecond},
		{MSS: 10, InitialWindow: 0, BufferCap: 100, ProbeInterval: -time.Millisecond},
	}
	for i, cfg := range bads {
		if _, err := NewScheduler(cfg); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("bad config %d: err = %v, want ErrInvalidParam", i, err)
		}
	}
}

// 满段与小段边界：恰好 M、M-1、M+1、2M。
func TestFullAndSmallBoundary(t *testing.T) {
	s := newTest(t, Config{MSS: 10, InitialWindow: 1000, BufferCap: 1000})

	// 恰好一个满段：立即发出。
	segs, err := s.Write(at(0), 10)
	checkSegs(t, segs, err, 10)
	checkState(t, s, 10, 0, 0)

	// M-1：在途非空，小段保留。
	segs, err = s.Write(at(1), 9)
	checkSegs(t, segs, err)
	checkState(t, s, 10, 9, 9)

	// 再写 1 字节凑满一个满段：满段发出。
	segs, err = s.Write(at(2), 1)
	checkSegs(t, segs, err, 10)
	checkState(t, s, 20, 0, 0)

	// M+1：满段发出，尾部 1 字节保留。
	segs, err = s.Write(at(3), 11)
	checkSegs(t, segs, err, 10)
	checkState(t, s, 30, 1, 1)

	// 在途全部确认后，保留的小段立即发出。
	segs, err = s.Ack(at(4), 30, 1000)
	checkSegs(t, segs, err, 1)
	checkState(t, s, 1, 0, 0)

	// 恰好 2M：两个满段。
	segs, err = s.Ack(at(5), 1, 1000)
	checkSegs(t, segs, err)
	segs, err = s.Write(at(6), 20)
	checkSegs(t, segs, err, 10, 10)
	checkState(t, s, 20, 0, 0)
}

// 默认模式下在途为空时小段立即发出。
func TestSmallSegmentImmediateWhenIdle(t *testing.T) {
	s := newTest(t, Config{MSS: 10, InitialWindow: 1000, BufferCap: 1000})
	segs, err := s.Write(at(0), 7)
	checkSegs(t, segs, err, 7)
	checkState(t, s, 7, 0, 0)
}

// 窗口恰好等于小段长度：整段发出；差一字节：只发窗口允许的部分。
func TestWindowEqualsSmallSegment(t *testing.T) {
	s := newTest(t, Config{MSS: 100, InitialWindow: 50, BufferCap: 1000})
	if _, err := s.SetNoDelay(at(0), true); err != nil {
		t.Fatal(err)
	}
	segs, err := s.Write(at(1), 50)
	checkSegs(t, segs, err, 50)
	checkState(t, s, 50, 0, 0)
}

func TestWindowOneByteShort(t *testing.T) {
	s := newTest(t, Config{MSS: 100, InitialWindow: 49, BufferCap: 1000})
	if _, err := s.SetNoDelay(at(0), true); err != nil {
		t.Fatal(err)
	}
	// 窗口比小段少一字节：发 49，余 1 字节留在缓冲。
	segs, err := s.Write(at(1), 50)
	checkSegs(t, segs, err, 49)
	checkState(t, s, 49, 1, 0)
	// 窗口再开一字节：余量发出。
	segs, err = s.Ack(at(2), 49, 1)
	checkSegs(t, segs, err, 1)
	checkState(t, s, 1, 0, 0)
}

// 被窗口截短的段不再视为满段：默认模式且在途非空时被保留。
func TestWindowTruncatedSegmentIsSmall(t *testing.T) {
	s := newTest(t, Config{MSS: 100, InitialWindow: 330, BufferCap: 1000})
	// 三个满段发出后，可用窗口只剩 30 < M。
	segs, err := s.Write(at(0), 300)
	checkSegs(t, segs, err, 100, 100, 100)
	// 再写 50：窗口只能容纳 30，被窗口截短的 30 字节是小段，
	// 在途非空，保留。
	segs, err = s.Write(at(1), 50)
	checkSegs(t, segs, err)
	checkState(t, s, 300, 50, 30)
	// 在途全部确认、窗口维持 30：截短小段放行，发 30，余 20。
	segs, err = s.Ack(at(2), 300, 30)
	checkSegs(t, segs, err, 30)
	checkState(t, s, 30, 20, 0)
	// 窗口再开 120：右边缘 = 450，可用 = 120，尾部 20 发出。
	segs, err = s.Ack(at(3), 30, 120)
	checkSegs(t, segs, err, 20)
	checkState(t, s, 20, 0, 0)
}

// 软木塞超时恰在边界：不足不发，恰满即发。
func TestCorkTimeoutBoundary(t *testing.T) {
	s := newTest(t, Config{
		MSS: 100, InitialWindow: 1000, BufferCap: 1000,
		CorkTimeout: 100 * time.Millisecond,
	})
	if _, err := s.SetCork(at(0), true); err != nil {
		t.Fatal(err)
	}
	// 在途为空也只发满段：50 字节小段被保留，锚点 = 1ms。
	segs, err := s.Write(at(1), 50)
	checkSegs(t, segs, err)
	checkState(t, s, 0, 50, 50)
	if d, ok := s.NextDeadline(); !ok || !d.Equal(at(101)) {
		t.Fatalf("NextDeadline = %v,%v, want %v,true", d, ok, at(101))
	}
	// 99ms < 超时：仍保留。
	segs, err = s.Advance(at(100))
	checkSegs(t, segs, err)
	checkState(t, s, 0, 50, 50)
	// 恰满 100ms：解除保留，发出。
	segs, err = s.Advance(at(101))
	checkSegs(t, segs, err, 50)
	checkState(t, s, 50, 0, 0)
	if _, ok := s.NextDeadline(); ok {
		t.Fatal("NextDeadline should be empty after release")
	}
}

// 两个选项同时打开时软木塞优先。
func TestCorkOverridesNoDelay(t *testing.T) {
	s := newTest(t, Config{
		MSS: 100, InitialWindow: 1000, BufferCap: 1000,
		CorkTimeout: 100 * time.Millisecond,
	})
	if _, err := s.SetNoDelay(at(0), true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetCork(at(1), true); err != nil {
		t.Fatal(err)
	}
	// 不延迟已开，但软木塞优先：小段仍保留。
	segs, err := s.Write(at(2), 50)
	checkSegs(t, segs, err)
	checkState(t, s, 0, 50, 50)
	// 软木塞到期后按窗口约束发出。
	segs, err = s.Advance(at(102))
	checkSegs(t, segs, err, 50)
	// 再次同时打开两选项，关闭软木塞立即解除保留。
	if _, err := s.SetCork(at(103), true); err != nil {
		t.Fatal(err)
	}
	segs, err = s.Write(at(104), 30)
	checkSegs(t, segs, err)
	checkState(t, s, 50, 30, 30)
	segs, err = s.SetCork(at(105), false)
	checkSegs(t, segs, err, 30)
	checkState(t, s, 80, 0, 0)
}

// 软木塞打开期间凑满满段：满段总是可发。
func TestCorkStillSendsFullSegments(t *testing.T) {
	s := newTest(t, Config{
		MSS: 10, InitialWindow: 1000, BufferCap: 1000,
		CorkTimeout: time.Hour,
	})
	if _, err := s.SetCork(at(0), true); err != nil {
		t.Fatal(err)
	}
	segs, err := s.Write(at(1), 25)
	checkSegs(t, segs, err, 10, 10)
	checkState(t, s, 20, 5, 5)
}

// 关闭后残余数据忽略保留约束，在窗口受限时分批发出，直至清空。
func TestCloseDrainsInBatches(t *testing.T) {
	s := newTest(t, Config{MSS: 10, InitialWindow: 25, BufferCap: 1000})
	// 窗口 25：发两个满段；被窗口截短的 5 字节因本批满段在途而保留。
	segs, err := s.Write(at(0), 60)
	checkSegs(t, segs, err, 10, 10)
	checkState(t, s, 20, 40, 5)
	// 关闭：忽略保留约束，截短的 5 字节立即发出。
	segs, err = s.Close(at(1))
	checkSegs(t, segs, err, 5)
	checkState(t, s, 25, 35, 0)
	// 确认 25、窗口 10：关闭后忽略保留，按窗口发出 10。
	segs, err = s.Ack(at(2), 25, 10)
	checkSegs(t, segs, err, 10)
	checkState(t, s, 10, 25, 0)
	// 确认 10、窗口 20：发两个满段。
	segs, err = s.Ack(at(3), 10, 20)
	checkSegs(t, segs, err, 10, 10)
	checkState(t, s, 20, 5, 0)
	// 确认 20、窗口 5：尾部 5 字节（小段）也发出，缓冲清空。
	segs, err = s.Ack(at(4), 20, 5)
	checkSegs(t, segs, err, 5)
	checkState(t, s, 5, 0, 0)
	// 关闭后写入被拒绝。
	if _, err = s.Write(at(5), 1); !errors.Is(err, ErrWriteAfterClose) {
		t.Fatalf("err = %v, want ErrWriteAfterClose", err)
	}
}

// 零窗口探测：到期开始、窗口打开后取消。
func TestZeroWindowProbe(t *testing.T) {
	s := newTest(t, Config{
		MSS: 10, InitialWindow: 0, BufferCap: 1000,
		ProbeInterval: 50 * time.Millisecond,
	})
	// 窗口为零、缓冲非空、在途为空：探测计时自窗口为零起算。
	segs, err := s.Write(at(0), 30)
	checkSegs(t, segs, err)
	checkState(t, s, 0, 30, 0)
	if d, ok := s.NextDeadline(); !ok || !d.Equal(at(50)) {
		t.Fatalf("NextDeadline = %v,%v, want %v,true", d, ok, at(50))
	}
	// 间隔未满：不探测。
	segs, err = s.Advance(at(49))
	checkSegs(t, segs, err)
	// 恰满间隔：发出一字节探测段，计入在途。
	segs, err = s.Advance(at(50))
	checkSegs(t, segs, err, 1)
	checkState(t, s, 1, 29, 0)
	// 在途非空：不再探测。
	if _, ok := s.NextDeadline(); ok {
		t.Fatal("no probe deadline expected while in-flight")
	}
	segs, err = s.Advance(at(200))
	checkSegs(t, segs, err)
	// 探测字节被确认、窗口仍为零：自上次探测（50ms）起重新计时。
	segs, err = s.Ack(at(210), 1, 0)
	checkSegs(t, segs, err, 1)
	checkState(t, s, 1, 28, 0)
}

// 探测字节被确认时距上次探测未满间隔：等到恰满间隔再探测。
func TestProbeReanchorAtLastProbe(t *testing.T) {
	s := newTest(t, Config{
		MSS: 10, InitialWindow: 0, BufferCap: 1000,
		ProbeInterval: 50 * time.Millisecond,
	})
	segs, err := s.Write(at(0), 30)
	checkSegs(t, segs, err)
	segs, err = s.Advance(at(50))
	checkSegs(t, segs, err, 1)
	// 60ms 确认：距上次探测仅 10ms，不立即探测。
	segs, err = s.Ack(at(60), 1, 0)
	checkSegs(t, segs, err)
	if d, ok := s.NextDeadline(); !ok || !d.Equal(at(100)) {
		t.Fatalf("NextDeadline = %v,%v, want %v,true", d, ok, at(100))
	}
	segs, err = s.Advance(at(99))
	checkSegs(t, segs, err)
	segs, err = s.Advance(at(100))
	checkSegs(t, segs, err, 1)
	checkState(t, s, 1, 28, 0)
}

// 探测字节被确认后，其余数据是否可发只取决于最新窗口。
func TestProbeAckThenWindowOpens(t *testing.T) {
	s := newTest(t, Config{
		MSS: 10, InitialWindow: 0, BufferCap: 1000,
		ProbeInterval: 50 * time.Millisecond,
	})
	segs, err := s.Write(at(0), 25)
	checkSegs(t, segs, err)
	segs, err = s.Advance(at(50))
	checkSegs(t, segs, err, 1)
	// 确认探测字节并开窗 20：其余数据按最新窗口发出。
	segs, err = s.Ack(at(60), 1, 20)
	checkSegs(t, segs, err, 10, 10)
	checkState(t, s, 20, 4, 0)
	// 确认全部在途、窗口通告 0：可用窗口始终为零（通告的窗口当场
	// 被数据填满），余下 4 字节自上次探测起满间隔再探测。
	segs, err = s.Ack(at(70), 20, 0)
	checkSegs(t, segs, err)
	checkState(t, s, 0, 4, 0)
	if d, ok := s.NextDeadline(); !ok || !d.Equal(at(100)) {
		t.Fatalf("NextDeadline = %v,%v, want %v,true", d, ok, at(100))
	}
	segs, err = s.Advance(at(100))
	checkSegs(t, segs, err, 1)
	checkState(t, s, 1, 3, 0)
}

// 窗口在探测到期前打开：探测取消，不再出现探测段。
func TestProbeCancelledByWindowOpen(t *testing.T) {
	s := newTest(t, Config{
		MSS: 10, InitialWindow: 0, BufferCap: 1000,
		ProbeInterval: 50 * time.Millisecond,
	})
	segs, err := s.Write(at(0), 5)
	checkSegs(t, segs, err)
	segs, err = s.Advance(at(30))
	checkSegs(t, segs, err)
	// 窗口打开：数据正常发出。
	segs, err = s.Ack(at(40), 0, 10)
	checkSegs(t, segs, err, 5)
	checkState(t, s, 5, 0, 0)
	// 之后推进时间：无探测。
	segs, err = s.Advance(at(1000))
	checkSegs(t, segs, err)
	if _, ok := s.NextDeadline(); ok {
		t.Fatal("no deadline expected")
	}
}

// 窗口因发送被用尽时也视为零窗口：在途确认完后按间隔探测。
func TestProbeAfterWindowConsumed(t *testing.T) {
	s := newTest(t, Config{
		MSS: 10, InitialWindow: 20, BufferCap: 1000,
		ProbeInterval: 50 * time.Millisecond,
	})
	segs, err := s.Write(at(0), 40)
	checkSegs(t, segs, err, 10, 10)
	checkState(t, s, 20, 20, 0)
	// 确认全部在途但窗口通告 0：右边缘不变，可用窗口为零。
	segs, err = s.Ack(at(10), 20, 0)
	checkSegs(t, segs, err)
	// 窗口自 0ms 起为零，10ms 未满间隔。
	if d, ok := s.NextDeadline(); !ok || !d.Equal(at(50)) {
		t.Fatalf("NextDeadline = %v,%v, want %v,true", d, ok, at(50))
	}
	segs, err = s.Advance(at(50))
	checkSegs(t, segs, err, 1)
	checkState(t, s, 1, 19, 0)
}

// 错误优先级：参数非法 > 时钟回退 > 关闭后写入 > 缓冲已满 >
// 窗口收缩 > 确认越界。
func TestRejectionOrder(t *testing.T) {
	newS := func() *Scheduler {
		return newTest(t, Config{MSS: 10, InitialWindow: 100, BufferCap: 50})
	}

	t.Run("InvalidBeforeClock", func(t *testing.T) {
		s := newS()
		if _, err := s.Write(at(10), 5); err != nil {
			t.Fatal(err)
		}
		// 长度非法且时钟回退：报参数非法。
		checkErr(t, func() error { _, err := s.Write(at(5), 0); return err }(), ErrInvalidParam)
	})
	t.Run("ClockBeforeClose", func(t *testing.T) {
		s := newS()
		if _, err := s.Close(at(10)); err != nil {
			t.Fatal(err)
		}
		// 时钟回退且关闭后写入：报时钟回退。
		checkErr(t, func() error { _, err := s.Write(at(5), 5); return err }(), ErrClockBackward)
	})
	t.Run("CloseBeforeBufferFull", func(t *testing.T) {
		s := newS()
		if _, err := s.Close(at(10)); err != nil {
			t.Fatal(err)
		}
		// 关闭后写入且会超缓冲上限：报关闭后写入。
		checkErr(t, func() error { _, err := s.Write(at(11), 100); return err }(), ErrWriteAfterClose)
	})
	t.Run("BufferFullAfterValidWrite", func(t *testing.T) {
		s := newTest(t, Config{MSS: 10, InitialWindow: 0, BufferCap: 50, ProbeInterval: time.Hour})
		if _, err := s.Write(at(0), 40); err != nil {
			t.Fatal(err)
		}
		checkErr(t, func() error { _, err := s.Write(at(1), 11); return err }(), ErrBufferFull)
	})
	t.Run("ShrinkBeforeAckRange", func(t *testing.T) {
		s := newS()
		// 确认越界且窗口收缩：报窗口收缩。
		checkErr(t, func() error { _, err := s.Ack(at(0), 5, 10); return err }(), ErrWindowShrink)
	})
	t.Run("AckRange", func(t *testing.T) {
		s := newS()
		// 在途为空，确认 1 字节：越界。
		checkErr(t, func() error { _, err := s.Ack(at(0), 1, 100); return err }(), ErrAckOutOfRange)
		// 负确认且窗口足够大不收缩：越界。
		checkErr(t, func() error { _, err := s.Ack(at(1), -1, 200); return err }(), ErrAckOutOfRange)
	})
	t.Run("InvalidAckWindow", func(t *testing.T) {
		s := newS()
		checkErr(t, func() error { _, err := s.Ack(at(0), 0, -1); return err }(), ErrInvalidParam)
	})
	t.Run("InvalidWriteLen", func(t *testing.T) {
		s := newS()
		checkErr(t, func() error { _, err := s.Write(at(0), 0); return err }(), ErrInvalidParam)
		checkErr(t, func() error { _, err := s.Write(at(0), -3); return err }(), ErrInvalidParam)
		checkErr(t, func() error { _, err := s.Write(at(0), MaxWriteLen+1); return err }(), ErrInvalidParam)
		if _, err := s.Write(at(0), MaxWriteLen); !errors.Is(err, ErrBufferFull) {
			t.Fatalf("err = %v, want ErrBufferFull", err)
		}
	})
}

// 被拒绝的事件不得改变缓冲、在途、窗口与时钟。
func TestRejectedEventKeepsState(t *testing.T) {
	s := newTest(t, Config{
		MSS: 10, InitialWindow: 30, BufferCap: 100,
		CorkTimeout:   100 * time.Millisecond,
		ProbeInterval: 50 * time.Millisecond,
	})
	if _, err := s.SetCork(at(0), true); err != nil {
		t.Fatal(err)
	}
	// 软木塞下满段立即可发：20 字节发出，5 字节小段保留。
	segs, err := s.Write(at(1), 25)
	checkSegs(t, segs, err, 10, 10)
	snapshot := func() string {
		d, ok := s.NextDeadline()
		return fmt.Sprintf("%d/%d/%d/%v/%v", s.InFlight(), s.Buffered(), s.Retained(), d, ok)
	}
	before := snapshot()
	rejections := []error{
		func() error { _, err := s.Write(at(1), 0); return err }(),     // 参数非法
		func() error { _, err := s.Write(at(0), 5); return err }(),     // 时钟回退
		func() error { _, err := s.Write(at(2), 200); return err }(),   // 缓冲已满
		func() error { _, err := s.Ack(at(2), 0, 1); return err }(),    // 窗口收缩
		func() error { _, err := s.Ack(at(2), 26, 100); return err }(), // 确认越界
	}
	for i, err := range rejections {
		if err == nil {
			t.Fatalf("rejection %d unexpectedly accepted", i)
		}
	}
	if after := snapshot(); after != before {
		t.Fatalf("state changed by rejections: %s -> %s", before, after)
	}
	// 时钟未被拒绝事件推进：软木塞仍按原锚点（1ms）到期。
	segs, err = s.Advance(at(101))
	checkSegs(t, segs, err, 5)
}

// 缓冲已满时整次写入被拒绝，不接受其中一部分。
func TestBufferFullIsAtomic(t *testing.T) {
	s := newTest(t, Config{MSS: 10, InitialWindow: 0, BufferCap: 50, ProbeInterval: time.Hour})
	if _, err := s.Write(at(0), 30); err != nil {
		t.Fatal(err)
	}
	checkErr(t, func() error { _, err := s.Write(at(1), 25); return err }(), ErrBufferFull)
	if got := s.Buffered(); got != 30 {
		t.Fatalf("Buffered = %d, want 30", got)
	}
	if _, err := s.Write(at(2), 20); err != nil {
		t.Fatal(err)
	}
	if got := s.Buffered(); got != 50 {
		t.Fatalf("Buffered = %d, want 50", got)
	}
	checkErr(t, func() error { _, err := s.Write(at(3), 1); return err }(), ErrBufferFull)
}

// 时钟回退：小于上一次被接受事件的时间报错，等于允许。
func TestClockBackward(t *testing.T) {
	s := newTest(t, Config{MSS: 10, InitialWindow: 100, BufferCap: 100})
	if _, err := s.Write(at(10), 5); err != nil {
		t.Fatal(err)
	}
	checkErr(t, func() error { _, err := s.Advance(at(9)); return err }(), ErrClockBackward)
	if _, err := s.Advance(at(10)); err != nil {
		t.Fatalf("equal time should be accepted: %v", err)
	}
	checkErr(t, func() error { _, err := s.Ack(at(9), 0, 100); return err }(), ErrClockBackward)
	checkErr(t, func() error { _, err := s.SetCork(at(8), true); return err }(), ErrClockBackward)
	checkErr(t, func() error { _, err := s.Close(at(7)); return err }(), ErrClockBackward)
}

// 查询不改变任何状态：反复查询后，软木塞仍恰在原定边界触发。
func TestQueriesAreReadOnly(t *testing.T) {
	s := newTest(t, Config{
		MSS: 10, InitialWindow: 100, BufferCap: 100,
		CorkTimeout: 100 * time.Millisecond,
	})
	if _, err := s.SetCork(at(0), true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write(at(1), 5); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_ = s.InFlight()
		_ = s.Buffered()
		_ = s.Retained()
		if d, ok := s.NextDeadline(); !ok || !d.Equal(at(101)) {
			t.Fatalf("NextDeadline = %v,%v, want %v,true", d, ok, at(101))
		}
	}
	segs, err := s.Advance(at(100))
	checkSegs(t, segs, err)
	segs, err = s.Advance(at(101))
	checkSegs(t, segs, err, 5)
}
