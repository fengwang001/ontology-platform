package uart

import (
	"errors"
	"math/rand"
	"runtime"
	"sync"
	"testing"
)

// ---- Naive bit-level waveform construction (independent reference) ----

type frameSpec struct {
	data    byte
	pbit    int // used only when parity != ParityNone
	stop    int // 1 normal, 0 low stop bit (framing error / break)
	leading int // idle high samples before the start bit
}

// buildWave emits samples one at a time following the frame format: leading
// idle highs, a low start bit, 8 LSB-first data bits, an optional parity bit
// and one stop bit; each bit occupies 16 samples.
func buildWave(p Parity, specs []frameSpec) []int {
	var levels []int
	for _, spec := range specs {
		for i := 0; i < spec.leading; i++ {
			levels = append(levels, 1)
		}
		bits := []int{0} // start
		for i := 0; i < 8; i++ {
			bits = append(bits, int(spec.data>>uint(i))&1)
		}
		if p != ParityNone {
			bits = append(bits, spec.pbit)
		}
		bits = append(bits, spec.stop)
		for _, b := range bits {
			for j := 0; j < 16; j++ {
				levels = append(levels, b)
			}
		}
	}
	return levels
}

// correctParity returns the parity bit making total ones even (even mode) or
// odd (odd mode).
func correctParity(p Parity, data byte) int {
	ones := 0
	for i := 0; i < 8; i++ {
		ones += int(data>>uint(i)) & 1
	}
	if p == ParityOdd {
		return ones&1 ^ 1
	}
	return ones & 1
}

func popcount(b byte) int {
	n := 0
	for i := 0; i < 8; i++ {
		n += int(b>>uint(i)) & 1
	}
	return n
}

// ---- Naive literal reference decoder ----

const (
	refIdle = iota
	refStart
	refData
	refWait
)

type refFrame struct {
	data        byte
	parityError bool
	frameError  bool
	breakFrame  bool
	endIndex    uint64
}

func naiveDecode(p Parity, depth int, levels []int) (frames []refFrame, glitches, overruns uint64) {
	state := refIdle
	var s uint64
	var data byte
	var pbit int
	frames = []refFrame{}

	finish := func(stopLevel, n uint64) {
		ones := 0
		for i := 0; i < 8; i++ {
			ones += int(data>>uint(i)) & 1
		}
		pe := false
		hasP := p != ParityNone
		if hasP {
			ones += pbit
			if p == ParityEven {
				pe = ones%2 != 0
			} else {
				pe = ones%2 == 0
			}
		}
		fe := stopLevel == 0
		brk := fe && data == 0 && (!hasP || pbit == 0)
		rf := refFrame{data, pe, fe, brk, n}
		if len(frames) >= depth {
			overruns++
		} else {
			frames = append(frames, rf)
		}
		if fe {
			state = refWait
		} else {
			state = refIdle
		}
	}

	for n, level := range levels {
		idx := uint64(n)
		switch state {
		case refIdle:
			if level == 0 {
				s = idx
				state = refStart
			}
		case refStart:
			if idx == s+7 {
				if level == 1 {
					glitches++
					state = refIdle
				} else {
					data, pbit = 0, 0
					state = refData
				}
			}
		case refData:
			for i := 0; i < 8; i++ {
				if idx == s+7+16*uint64(i+1) && level == 1 {
					data |= 1 << uint(i)
				}
			}
			if p == ParityNone {
				if idx == s+7+16*9 {
					finish(uint64(level), idx)
				}
			} else if idx == s+7+16*9 {
				pbit = level
			} else if idx == s+7+16*10 {
				finish(uint64(level), idx)
			}
		case refWait:
			if level == 1 {
				state = refIdle
			}
		}
	}
	return
}

func drain(t *testing.T, r *Receiver) []Frame {
	t.Helper()
	var out []Frame
	for {
		f, err := r.Pop()
		if errors.Is(err, ErrQueueEmpty) {
			return out
		}
		if err != nil {
			t.Fatalf("unexpected pop error: %v", err)
		}
		out = append(out, f)
	}
}

func logWave(t *testing.T, label string, levels []int) {
	t.Helper()
	line := ""
	for _, l := range levels {
		if l == 1 {
			line += "1"
		} else {
			line += "0"
		}
	}
	t.Logf("[%s] levels(%d)=%s", label, len(levels), line)
}

func logFrames(t *testing.T, label string, frames []Frame) {
	t.Helper()
	for _, f := range frames {
		t.Logf("[%s] frame data=0x%02X parityErr=%t frameErr=%t break=%t endIndex=%d",
			label, f.Data, f.ParityError, f.FrameError, f.Break, f.EndIndex)
	}
}

// TestGlitchAndValidStart covers the s+7 decision: high => glitch and back
// to idle, low => confirmed start producing a frame.
func TestGlitchAndValidStart(t *testing.T) {
	levels := []int{}
	for i := 0; i < 3; i++ { // idle highs
		levels = append(levels, 1)
	}
	for i := 3; i < 10; i++ { // low candidate starting at s=3
		levels = append(levels, 0)
	}
	levels = append(levels, 1) // index 10 == s+7 -> glitch
	for i := 11; i < 40; i++ {
		levels = append(levels, 1)
	}
	logWave(t, "glitch", levels)

	r, err := New(ParityNone, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.FeedAll(levels); err != nil {
		t.Fatal(err)
	}
	if got := r.GlitchCount(); got != 1 {
		t.Fatalf("glitch count = %d, want 1", got)
	}
	if r.Len() != 0 || r.SampleIndex() != 40 {
		t.Fatalf("len=%d index=%d, want 0,40", r.Len(), r.SampleIndex())
	}
	t.Logf("判定依据: s=3, s+7=10 处为高 => 毛刺 glitches=%d, 该采样不作新起始候选, 回到空闲",
		r.GlitchCount())

	// Confirmed start: frame of 0x00, low at s+7, high stop.
	good := buildWave(ParityNone, []frameSpec{{data: 0x00, stop: 1, leading: 2}})
	logWave(t, "valid", good)
	r2, _ := New(ParityNone, 4)
	if err := r2.FeedAll(good); err != nil {
		t.Fatal(err)
	}
	frames := drain(t, r2)
	logFrames(t, "valid", frames)
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	f := frames[0]
	if f.Data != 0 || f.ParityError || f.FrameError || f.Break {
		t.Fatalf("unexpected frame: %+v", f)
	}
	if want := uint64(2 + 7 + 16*9); f.EndIndex != want {
		t.Fatalf("end index = %d, want %d", f.EndIndex, want)
	}
	t.Logf("判定依据: s=2, s+7=9 为低 => 起始成立; 停止位在 endIndex=%d 采到高 => 正常帧",
		f.EndIndex)
}

// TestSampleIndices verifies the exact midpoint index of every data bit,
// the parity bit and the stop bit (with and without parity).
func TestSampleIndices(t *testing.T) {
	base := buildWave(ParityEven, []frameSpec{{data: 0x00, pbit: 0, stop: 1}})

	for i := 0; i < 8; i++ {
		levels := append([]int(nil), base...)
		idx := 7 + 16*(i+1)
		levels[idx] = 1 // flip exactly the midpoint sample
		r, _ := New(ParityEven, 2)
		if err := r.FeedAll(levels); err != nil {
			t.Fatal(err)
		}
		frames := drain(t, r)
		if len(frames) != 1 || frames[0].Data != 1<<uint(i) {
			t.Fatalf("flip midpoint of data bit %d at index %d: %+v", i, idx, frames)
		}
		t.Logf("数据位 %d 在序号 s+7+16*%d=%d 采样; 翻转该点 => 0x%02X",
			i, i+1, idx, frames[0].Data)
	}

	levels := append([]int(nil), base...)
	levels[7+16*9] = 1 // parity midpoint
	r, _ := New(ParityEven, 2)
	if err := r.FeedAll(levels); err != nil {
		t.Fatal(err)
	}
	frames := drain(t, r)
	if !frames[0].ParityError || frames[0].EndIndex != 7+16*10 {
		t.Fatalf("parity midpoint flip: %+v", frames)
	}
	t.Logf("校验位采样序号 s+7+16*9=%d; 停止位采样序号 s+7+16*10=%d (带校验)",
		7+16*9, 7+16*10)

	np := buildWave(ParityNone, []frameSpec{{data: 0x00, stop: 1}})
	r2, _ := New(ParityNone, 2)
	_ = r2.FeedAll(np)
	if f := drain(t, r2)[0]; f.EndIndex != 7+16*9 {
		t.Fatalf("no-parity stop index = %d, want %d", f.EndIndex, 7+16*9)
	}
	t.Logf("无校验时停止位采样序号 s+7+16*9=%d", 7+16*9)
}

// TestParityFlags covers even/odd modes on 0x00 and 0xFF with correct and
// corrupted parity bits.
func TestParityFlags(t *testing.T) {
	cases := []struct {
		name   string
		p      Parity
		data   byte
		pbit   int
		wantPE bool
	}{
		{"even 0x00 correct", ParityEven, 0x00, 0, false},
		{"even 0x00 wrong", ParityEven, 0x00, 1, true},
		{"even 0xFF correct", ParityEven, 0xFF, 0, false},
		{"even 0xFF wrong", ParityEven, 0xFF, 1, true},
		{"odd 0x00 correct", ParityOdd, 0x00, 1, false},
		{"odd 0x00 wrong", ParityOdd, 0x00, 0, true},
		{"odd 0xFF correct", ParityOdd, 0xFF, 1, false},
		{"odd 0xFF wrong", ParityOdd, 0xFF, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			levels := buildWave(tc.p, []frameSpec{{data: tc.data, pbit: tc.pbit, stop: 1}})
			logWave(t, tc.name, levels)
			r, _ := New(tc.p, 2)
			if err := r.FeedAll(levels); err != nil {
				t.Fatal(err)
			}
			f := drain(t, r)[0]
			if f.Data != tc.data || f.ParityError != tc.wantPE || f.FrameError || f.Break {
				t.Fatalf("%s: frame=%+v want parityErr=%t", tc.name, f, tc.wantPE)
			}
			t.Logf("%s: 数据0x%02X 含%d个1, 校验位%d => 合计%d个1, parityErr=%t",
				tc.name, tc.data, popcount(tc.data), tc.pbit,
				popcount(tc.data)+tc.pbit, f.ParityError)
		})
	}
}

// TestFrameErrorNotBreak: low stop with non-zero data is a framing error
// without a break flag.
func TestFrameErrorNotBreak(t *testing.T) {
	levels := buildWave(ParityNone, []frameSpec{{data: 0x01, stop: 0, leading: 1}})
	logWave(t, "frame-error", levels)
	r, _ := New(ParityNone, 4)
	_ = r.FeedAll(levels)
	f := drain(t, r)[0]
	if !f.FrameError || f.Break || f.Data != 0x01 {
		t.Fatalf("frame=%+v, want frameErr=true break=false", f)
	}
	t.Logf("判定依据: 停止位=%d 低 => frameErr; 数据非全零(0x01) => break=false, endIndex=%d",
		0, f.EndIndex)
}

// TestBreakOddParityAndWaitHigh: an all-zero frame under odd parity carries
// both parity error and break (which implies frame error), and reception of
// the next start requires a high sample first.
func TestBreakOddParityAndWaitHigh(t *testing.T) {
	first := buildWave(ParityOdd, []frameSpec{{data: 0x00, pbit: 0, stop: 0}})
	second := buildWave(ParityOdd, []frameSpec{{data: 0x00, pbit: 1, stop: 1}})
	// First frame is 11 bits x16 = 176 samples (start, 8 data, parity, stop).
	// Append some ignored lows while in wait state, one recovery high, then
	// the next frame.
	var withHigh []int
	withHigh = append(withHigh, first...)
	withHigh = append(withHigh, 0, 0, 0, 0) // ignored while waiting for high
	withHigh = append(withHigh, 1)          // leaves wait state; no new start
	withHigh = append(withHigh, second...)
	logWave(t, "break-waithigh", withHigh)

	r, _ := New(ParityOdd, 8)
	if err := r.FeedAll(withHigh); err != nil {
		t.Fatal(err)
	}
	frames := drain(t, r)
	logFrames(t, "break-waithigh", frames)
	if len(frames) != 2 {
		t.Fatalf("frames=%d, want 2", len(frames))
	}
	b := frames[0]
	if !b.Break || !b.FrameError || !b.ParityError || b.Data != 0 {
		t.Fatalf("break frame=%+v, want break/frameErr/parityErr all true", b)
	}
	t.Logf("判定依据: 全零数据+校验位0 在奇校验下合计0个1 => parityErr; 停止位低且全零 => break+frameErr, endIndex=%d",
		b.EndIndex)

	// Without the separating high sample, the second frame is missed.
	r2, _ := New(ParityOdd, 8)
	noHigh := append(append([]int(nil), first...), 0, 0, 0, 0)
	noHigh = append(noHigh, second...)
	_ = r2.FeedAll(noHigh)
	if got := drain(t, r2); len(got) != 1 {
		t.Fatalf("without recovery high: frames=%d, want 1 (still in wait state)", len(got))
	}
	t.Logf("判定依据: 中止后持续低电平均不产生新起始; 直到见到高电平才回空闲, 随后第二帧正常收到")
}

// TestHighStopThenImmediateLow: a low sample right after a high stop is the
// next start candidate.
func TestHighStopThenImmediateLow(t *testing.T) {
	first := buildWave(ParityNone, []frameSpec{{data: 0xA5, stop: 1, leading: 0}})
	second := buildWave(ParityNone, []frameSpec{{data: 0x5A, stop: 1, leading: 0}})
	levels := append(first, second...) // zero idle gap between frames
	logWave(t, "backtoback", levels)
	r, _ := New(ParityNone, 4)
	if err := r.FeedAll(levels); err != nil {
		t.Fatal(err)
	}
	frames := drain(t, r)
	logFrames(t, "backtoback", frames)
	if len(frames) != 2 || frames[0].Data != 0xA5 || frames[1].Data != 0x5A {
		t.Fatalf("frames=%+v, want 0xA5 then 0x5A", frames)
	}
	if frames[1].EndIndex != frames[0].EndIndex+160 {
		t.Fatalf("second end index=%d, want %d", frames[1].EndIndex, frames[0].EndIndex+160)
	}
	t.Logf("判定依据: 高停止位后直接回空闲, 紧接的低电平序号 s=%d 成为下一帧起始候选",
		frames[0].EndIndex+1)
}

// TestQueueOverrun: with a full queue the new frame is dropped and counted,
// old frames stay intact.
func TestQueueOverrun(t *testing.T) {
	r, _ := New(ParityNone, 2)
	var all []int
	for i := 0; i < 4; i++ {
		all = append(all, buildWave(ParityNone, []frameSpec{{
			data: byte(0x10 + i), stop: 1,
		}})...)
	}
	logWave(t, "overrun", all)
	if err := r.FeedAll(all); err != nil {
		t.Fatal(err)
	}
	if r.Len() != 2 {
		t.Fatalf("len=%d, want 2", r.Len())
	}
	if r.OverrunCount() != 2 || r.ProducedCount() != 4 {
		t.Fatalf("overruns=%d produced=%d, want 2,4", r.OverrunCount(), r.ProducedCount())
	}
	frames := drain(t, r)
	if frames[0].Data != 0x10 || frames[1].Data != 0x11 {
		t.Fatalf("old frames changed: %+v", frames)
	}
	if got := uint64(len(frames)) + r.OverrunCount(); got != r.ProducedCount() {
		t.Fatalf("invariant: enqueued+overrun=%d != produced=%d", got, r.ProducedCount())
	}
	t.Logf("判定依据: 队列深度2, 前两帧0x10/0x11保留, 后两帧丢弃, overruns=%d produced=%d",
		r.OverrunCount(), r.ProducedCount())
}

// TestRejections verifies distinguishable errors and atomic rejection: no
// state or sample-index change.
func TestRejections(t *testing.T) {
	if _, err := New(Parity(99), 4); !errors.Is(err, ErrInvalidParity) {
		t.Fatalf("invalid parity err=%v", err)
	}
	if _, err := New(ParityNone, 0); !errors.Is(err, ErrInvalidDepth) {
		t.Fatalf("invalid depth err=%v", err)
	}

	r, _ := New(ParityNone, 2)
	if err := r.Feed(2); !errors.Is(err, ErrInvalidLevel) {
		t.Fatalf("Feed err=%v", err)
	}
	if err := r.Feed(-1); !errors.Is(err, ErrInvalidLevel) {
		t.Fatalf("Feed err=%v", err)
	}
	// Whole-batch rejection: nothing is fed even before the bad element.
	good := buildWave(ParityNone, []frameSpec{{data: 0x00, stop: 1}})
	bad := append(append([]int(nil), good...), 5)
	if err := r.FeedAll(bad); !errors.Is(err, ErrInvalidLevel) {
		t.Fatalf("FeedAll err=%v", err)
	}
	if r.SampleIndex() != 0 || r.Len() != 0 {
		t.Fatalf("state changed after rejected batch: index=%d len=%d",
			r.SampleIndex(), r.Len())
	}
	// Bad element placed in the middle must also reject the whole batch.
	mid := append(append([]int(nil), good[:5]...), 7)
	mid = append(mid, good[5:]...)
	if err := r.FeedAll(mid); !errors.Is(err, ErrInvalidLevel) {
		t.Fatalf("FeedAll mid err=%v", err)
	}
	if r.SampleIndex() != 0 {
		t.Fatalf("index=%d after mid-batch rejection", r.SampleIndex())
	}
	if _, err := r.Pop(); !errors.Is(err, ErrQueueEmpty) {
		t.Fatalf("Pop err=%v", err)
	}
	t.Logf("判定依据: 非法校验模式/深度/电平/空队列分别返回独立哨兵错误; FeedAll 先整批校验, 拒绝时序号仍为0")
}

// feedAllSplit feeds levels as chunks split at cut positions.
func feedAllSplit(r *Receiver, levels []int, cuts []int) {
	start := 0
	for _, c := range cuts {
		_ = r.FeedAll(levels[start:c])
		start = c
	}
	_ = r.FeedAll(levels[start:])
}

// TestAllSplitsEquivalent: every possible single cut (and all multi-cuts for
// a short stream) gives identical frames, indices, glitches and overruns.
func TestAllSplitsEquivalent(t *testing.T) {
	levels := buildWave(ParityEven, []frameSpec{
		{data: 0x3C, pbit: correctParity(ParityEven, 0x3C), stop: 1, leading: 3},
		{data: 0x00, pbit: 0, stop: 0, leading: 0}, // break
	})
	levels = append(levels, 1) // recovery high
	levels = append(levels, buildWave(ParityEven, []frameSpec{
		{data: 0xF0, pbit: correctParity(ParityEven, 0xF0), stop: 1, leading: 0},
	})...)
	logWave(t, "splits", levels)

	type snapshot struct {
		frames   []Frame
		index    uint64
		glitches uint64
		overruns uint64
	}
	snap := func(r *Receiver) snapshot {
		return snapshot{drain(t, r), r.SampleIndex(), r.GlitchCount(), r.OverrunCount()}
	}

	var want snapshot
	{
		r, _ := New(ParityEven, 8)
		_ = r.FeedAll(levels)
		want = snap(r)
	}

	for cut := 1; cut < len(levels); cut++ {
		r, _ := New(ParityEven, 8)
		feedAllSplit(r, levels, []int{cut})
		got := snap(r)
		if !framesEqual(got.frames, want.frames) || got.index != want.index ||
			got.glitches != want.glitches || got.overruns != want.overruns {
			t.Fatalf("single cut at %d differs:\n got=%+v\nwant=%+v", cut, got, want)
		}
	}

	// Every pair of cuts for the short stream.
	for c1 := 1; c1 < len(levels); c1++ {
		for c2 := c1 + 1; c2 < len(levels); c2++ {
			r, _ := New(ParityEven, 8)
			feedAllSplit(r, levels, []int{c1, c2})
			got := snap(r)
			if !framesEqual(got.frames, want.frames) {
				t.Fatalf("cuts %d,%d differ: %+v vs %+v", c1, c2, got.frames, want.frames)
			}
		}
	}

	// Per-sample Feed is also identical to batches.
	r, _ := New(ParityEven, 8)
	for _, l := range levels {
		_ = r.Feed(l)
	}
	if got := snap(r); !framesEqual(got.frames, want.frames) {
		t.Fatalf("per-sample feed differs: %+v vs %+v", got, want)
	}
	t.Logf("判定依据: 全部 %d 个单切点与全部双切点以及逐点 Feed 的帧/序号/毛刺/溢出完全一致",
		len(levels)-1)
}

func framesEqual(a, b []Frame) bool {
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

// genRandomStream builds a random level stream by chaining random events:
// idle gaps, glitches (short lows back high before s+7), well-formed frames,
// breaks with low stops, random parity corruption, and random low/high after
// framing errors. Returns the raw levels and the specs used.
func genRandomStream(rng *rand.Rand, p Parity) []int {
	var levels []int
	stateWait := false
	n := rng.Intn(12) + 6
	for ev := 0; ev < n; ev++ {
		if stateWait {
			// Some low samples (must be ignored), then a mandatory high.
			lows := rng.Intn(20)
			for i := 0; i < lows; i++ {
				levels = append(levels, 0)
			}
			levels = append(levels, 1)
			gap := rng.Intn(5)
			for i := 0; i < gap; i++ {
				levels = append(levels, 1)
			}
			stateWait = false
			continue
		}
		gap := rng.Intn(8)
		for i := 0; i < gap; i++ {
			levels = append(levels, 1)
		}
		switch rng.Intn(3) {
		case 0: // glitch: 8 low samples (s..s+7 has s+7 low)... instead emit
			// lows at s..s+6 then a high at s+7, which rejects the candidate.
			for i := 0; i < 7; i++ { // s..s+6 all low
				levels = append(levels, 0)
			}
			levels = append(levels, 1) // index s+7 high -> glitch
			for i := 0; i < rng.Intn(5); i++ {
				levels = append(levels, 1)
			}
		case 1, 2: // frame
			data := byte(rng.Intn(256))
			spec := frameSpec{data: data, stop: 1}
			if p != ParityNone {
				spec.pbit = correctParity(p, data)
				if rng.Intn(3) == 0 {
					spec.pbit ^= 1
				}
			}
			if rng.Intn(5) == 0 {
				spec.data = 0
				if p != ParityNone {
					spec.pbit = 0
				}
			}
			if rng.Intn(4) == 0 {
				spec.stop = 0
				stateWait = true
			}
			levels = append(levels, buildWave(p, []frameSpec{spec})...)
		}
	}
	if stateWait {
		for i := 0; i < 3; i++ {
			levels = append(levels, 0)
		}
		levels = append(levels, 1)
	}
	return levels
}

// TestNaiveCrossCheck compares the implementation against the literal naive
// decoder over many random streams, parity modes and small queue depths.
func TestNaiveCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 300; iter++ {
		p := []Parity{ParityNone, ParityEven, ParityOdd}[rng.Intn(3)]
		depth := rng.Intn(3) + 1
		levels := genRandomStream(rng, p)

		r, _ := New(p, depth)
		// Random split boundaries per iteration.
		var pos int
		for pos < len(levels) {
			step := rng.Intn(40) + 1
			end := pos + step
			if end > len(levels) {
				end = len(levels)
			}
			if err := r.FeedAll(levels[pos:end]); err != nil {
				t.Fatalf("iter %d: %v", iter, err)
			}
			pos = end
		}
		got := drain(t, r)

		wantFrames, wantGlitch, wantOver := naiveDecode(p, depth, levels)
		if len(got) != len(wantFrames) {
			t.Fatalf("iter %d (p=%v D=%d levels=%d): got %d frames, naive %d; "+
				"impl glitches=%d over=%d naive glitches=%d over=%d",
				iter, p, depth, len(levels), len(got), len(wantFrames),
				r.GlitchCount(), r.OverrunCount(), wantGlitch, wantOver)
		}
		for i := range got {
			w := wantFrames[i]
			if got[i].Data != w.data || got[i].ParityError != w.parityError ||
				got[i].FrameError != w.frameError || got[i].Break != w.breakFrame ||
				got[i].EndIndex != w.endIndex {
				t.Fatalf("iter %d frame %d mismatch:\n impl=%+v\n naive=%+v",
					iter, i, got[i], w)
			}
		}
		if r.GlitchCount() != wantGlitch || r.OverrunCount() != wantOver {
			t.Fatalf("iter %d counters: impl g=%d o=%d naive g=%d o=%d",
				iter, r.GlitchCount(), r.OverrunCount(), wantGlitch, wantOver)
		}
		if r.ProducedCount() != uint64(len(got))+r.OverrunCount() {
			t.Fatalf("iter %d produced invariant broken", iter)
		}
		if r.SampleIndex() != uint64(len(levels)) {
			t.Fatalf("iter %d index=%d want %d", iter, r.SampleIndex(), len(levels))
		}
		if iter == 0 {
			logWave(t, "naive-seed0", levels)
			logFrames(t, "naive-seed0", got)
			t.Logf("判定依据: 与按规则书写的逐位朴素解码器对照, 字节/三种标志/endIndex/毛刺/溢出全部一致")
		}
	}
}

// TestDeterministicReplay: the same level sequence replayed yields identical
// results; SampleIndex always equals successfully fed count.
func TestDeterministicReplay(t *testing.T) {
	levels := genRandomStream(rand.New(rand.NewSource(7)), ParityOdd)
	run := func() ([]Frame, uint64, uint64) {
		r, _ := New(ParityOdd, 3)
		if err := r.FeedAll(levels); err != nil {
			t.Fatal(err)
		}
		return drain(t, r), r.GlitchCount(), r.OverrunCount()
	}
	a, ag, ao := run()
	b, bg, bo := run()
	if !framesEqual(a, b) || ag != bg || ao != bo {
		t.Fatalf("replay differs")
	}
	logFrames(t, "replay", a)
	t.Logf("判定依据: 同一序列重放两次, %d 帧与毛刺/溢出完全相同; SampleIndex 恒等于送入点数", len(a))
}

// TestConcurrentFeedPop: concurrent feeders and poppers are race-free under
// -race, and the serial-equivalent invariant holds when all feeders finish.
func TestConcurrentFeedPop(t *testing.T) {
	r, _ := New(ParityNone, 4)
	var producers sync.WaitGroup
	for w := 0; w < 4; w++ {
		wave := buildWave(ParityNone, []frameSpec{
			{data: byte(0x20 + w), stop: 1, leading: 1},
		})
		producers.Add(1)
		go func(seq []int) {
			defer producers.Done()
			for _, l := range seq {
				_ = r.Feed(l)
			}
		}(wave)
	}
	var popped int
	stop := make(chan struct{})
	var consumer sync.WaitGroup
	consumer.Add(1)
	go func() {
		defer consumer.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := r.Pop(); err == nil {
				popped++
			} else {
				runtime.Gosched() // back off while the queue is empty
			}
		}
	}()
	producers.Wait()
	// Drain remaining frames after feeding completes.
	for r.Len() > 0 {
		if _, err := r.Pop(); err == nil {
			popped++
		}
	}
	close(stop)
	consumer.Wait()
	if r.ProducedCount() != r.OverrunCount()+uint64(popped) {
		t.Fatalf("invariant: produced=%d overrun=%d popped=%d",
			r.ProducedCount(), r.OverrunCount(), popped)
	}
	t.Logf("判定依据: 并发 Feed/Pop 经 -race 无竞争; 产出 %d = 取出 %d + 溢出 %d",
		r.ProducedCount(), popped, r.OverrunCount())
}
