package uart

import (
	"sync"
	"testing"
)

func TestNewReceiverRejectsInvalidArguments(t *testing.T) {
	if _, err := NewReceiver(ParityMode(99), 2); err != ErrInvalidParity {
		t.Fatalf("invalid parity error = %v, want %v", err, ErrInvalidParity)
	}
	if _, err := NewReceiver(ParityNone, 0); err != ErrInvalidQueueDepth {
		t.Fatalf("invalid depth error = %v, want %v", err, ErrInvalidQueueDepth)
	}
}

func TestGlitchAtStartMidpointAndValidStart(t *testing.T) {
	receiver, err := NewReceiver(ParityNone, 4)
	if err != nil {
		t.Fatal(err)
	}

	levels := make([]int, 180)
	for i := range levels {
		levels[i] = 1
	}
	levels[8] = 0
	for i := 20; i < 180; i++ {
		levels[i] = 0
	}

	t.Logf("input=%v; sample 8 is a low candidate and sample 15 is high; sample 20 starts a break-length low frame", levels)
	if err := receiver.FeedAll(levels); err != nil {
		t.Fatal(err)
	}

	if got := receiver.GlitchCount(); got != 1 {
		t.Fatalf("GlitchCount() = %d, want 1", got)
	}
	if got := receiver.SampleCount(); got != 180 {
		t.Fatalf("SampleCount() = %d, want 180", got)
	}
	if receiver.state != stateWaitHigh {
		t.Fatalf("state = %d, want waitHigh after a low stop sample", receiver.state)
	}
	t.Logf("output: glitches=%d, state=waitHigh; basis: start candidate s=8, s+7=15 sampled high", receiver.GlitchCount())
}

func TestSamplingInstantsAndFramingNonBreak(t *testing.T) {
	receiver, err := NewReceiver(ParityEven, 4)
	if err != nil {
		t.Fatal(err)
	}

	value := byte(0b10100011)
	levels := make([]int, 176)
	for i := range levels {
		levels[i] = 1
	}
	for i := 0; i <= 7; i++ {
		levels[i] = 0
	}
	for bit := 0; bit < 8; bit++ {
		levels[7+16*(bit+1)] = int((value >> bit) & 1)
	}
	levels[7+16*9] = 0
	levels[7+16*10] = 0

	t.Logf("input defaults high except start s=0 through s+7=7 and exact sample indexes; data=%08b", value)
	if err := receiver.FeedAll(levels); err != nil {
		t.Fatal(err)
	}
	frame, err := receiver.Pop()
	if err != nil {
		t.Fatal(err)
	}

	if frame.Byte != value {
		t.Fatalf("Byte = %08b, want %08b", frame.Byte, value)
	}
	if frame.ParityError {
		t.Fatal("ParityError = true, want false for even parity with zero parity bit")
	}
	if !frame.FramingError || frame.Break {
		t.Fatalf("flags = parity%v framing%v break%v, want framing-only", frame.ParityError, frame.FramingError, frame.Break)
	}
	if frame.EndSampleIndex != 167 {
		t.Fatalf("EndSampleIndex = %d, want 167", frame.EndSampleIndex)
	}
	t.Logf("output=%+v; basis: data indexes 23,39,...,135; parity=151; stop=167", frame)
}

func TestParityFlagsForAllZeroAndAllOne(t *testing.T) {
	tests := []struct {
		name      string
		parity    ParityMode
		value     byte
		parityBit int
		wantError bool
	}{
		{"even zero correct", ParityEven, 0x00, 0, false},
		{"even zero wrong", ParityEven, 0x00, 1, true},
		{"even all ones correct", ParityEven, 0xff, 0, false},
		{"even all ones wrong", ParityEven, 0xff, 1, true},
		{"odd zero correct", ParityOdd, 0x00, 1, false},
		{"odd zero wrong", ParityOdd, 0x00, 0, true},
		{"odd all ones correct", ParityOdd, 0xff, 1, false},
		{"odd all ones wrong", ParityOdd, 0xff, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receiver, err := NewReceiver(tt.parity, 8)
			if err != nil {
				t.Fatal(err)
			}
			levels := waveformFrameWithParity(tt.value, tt.parity, tt.parityBit, 1)
			t.Logf("input byte=%08b parityBit=%d ones=%d; output expected parityError=%v", tt.value, tt.parityBit, onesCount(tt.value), tt.wantError)
			if err := receiver.FeedAll(levels); err != nil {
				t.Fatal(err)
			}
			frame, err := receiver.Pop()
			if err != nil {
				t.Fatal(err)
			}
			if frame.ParityError != tt.wantError {
				t.Fatalf("ParityError = %v, want %v", frame.ParityError, tt.wantError)
			}
		})
	}
}

func TestOddParityBreakWaitsForHigh(t *testing.T) {
	receiver, err := NewReceiver(ParityOdd, 4)
	if err != nil {
		t.Fatal(err)
	}

	levels := waveformFrameWithParity(0, ParityOdd, 0, 0)
	levels = append(levels, 0, 0, 0, 1)
	levels = append(levels, waveformFrame(0x42, ParityOdd, 1)...)

	t.Logf("input: odd-parity all-zero low stop, three ignored lows, one recovery high, then 0x42")
	if err := receiver.FeedAll(levels); err != nil {
		t.Fatal(err)
	}

	first, err := receiver.Pop()
	if err != nil {
		t.Fatal(err)
	}
	if !first.ParityError || !first.FramingError || !first.Break {
		t.Fatalf("first flags = parity%v framing%v break%v, want all true", first.ParityError, first.FramingError, first.Break)
	}
	second, err := receiver.Pop()
	if err != nil {
		t.Fatalf("second frame was not received after recovery high: %v", err)
	}
	if second.Byte != 0x42 || second.FramingError || second.Break || second.ParityError {
		t.Fatalf("second frame = %+v, want clean 0x42", second)
	}
	t.Logf("output first=%+v second=%+v; break waits for a high that cannot start a frame", first, second)
}

func TestNoParityAllZeroBreakHasNoParityError(t *testing.T) {
	receiver, err := NewReceiver(ParityNone, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := receiver.FeedAll(waveformFrame(0, ParityNone, 0)); err != nil {
		t.Fatal(err)
	}
	if err := receiver.Feed(1); err != nil {
		t.Fatal(err)
	}
	frame, err := receiver.Pop()
	if err != nil {
		t.Fatal(err)
	}
	if frame.ParityError || !frame.FramingError || !frame.Break {
		t.Fatalf("frame=%+v, want framing and break without parity error", frame)
	}
	t.Logf("input no-parity all-zero low stop; output=%+v; basis: no parity mode never sets parity error", frame)
}

func TestHighStopFollowedImmediatelyByNextStart(t *testing.T) {
	receiver, err := NewReceiver(ParityNone, 4)
	if err != nil {
		t.Fatal(err)
	}
	levels := append(waveformFrame(0x12, ParityNone, 1), waveformFrame(0x34, ParityNone, 1)...)
	if err := receiver.FeedAll(levels); err != nil {
		t.Fatal(err)
	}

	for _, want := range []byte{0x12, 0x34} {
		frame, err := receiver.Pop()
		if err != nil {
			t.Fatal(err)
		}
		if frame.Byte != want {
			t.Fatalf("Byte = %#x, want %#x; frame=%+v", frame.Byte, want, frame)
		}
		t.Logf("output frame=%+v; next low immediately after stop bits is a fresh start candidate", frame)
	}
}

func TestQueueOverflowKeepsOldFrame(t *testing.T) {
	receiver, err := NewReceiver(ParityNone, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := receiver.FeedAll(waveformFrame(0x11, ParityNone, 1)); err != nil {
		t.Fatal(err)
	}
	if err := receiver.FeedAll(waveformFrame(0x22, ParityNone, 1)); err != nil {
		t.Fatal(err)
	}

	if got := receiver.OverflowCount(); got != 1 {
		t.Fatalf("OverflowCount = %d, want 1", got)
	}
	first, err := receiver.Pop()
	if err != nil {
		t.Fatal(err)
	}
	if first.Byte != 0x11 {
		t.Fatalf("queued byte = %#x, want old frame 0x11", first.Byte)
	}
	if err := receiver.FeedAll(waveformFrame(0x33, ParityNone, 1)); err != nil {
		t.Fatal(err)
	}
	third, err := receiver.Pop()
	if err != nil {
		t.Fatal(err)
	}
	if third.Byte != 0x33 || receiver.ProducedCount() != 3 || receiver.OverflowCount() != 1 {
		t.Fatalf("third=%+v produced=%d overflow=%d", third, receiver.ProducedCount(), receiver.OverflowCount())
	}
	t.Logf("output old=%#x acceptedAfterPop=%#x produced=%d overflow=%d", first.Byte, third.Byte, receiver.ProducedCount(), receiver.OverflowCount())
}

func TestInvalidOperationsAreAtomic(t *testing.T) {
	receiver, err := NewReceiver(ParityNone, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := receiver.Feed(0); err != nil {
		t.Fatal(err)
	}
	beforeSamples := receiver.SampleCount()
	if err := receiver.Feed(2); err != ErrInvalidLevel {
		t.Fatalf("Feed invalid error = %v, want %v", err, ErrInvalidLevel)
	}

	batch := append(waveformFrame(1, ParityNone, 1), 2)
	if err := receiver.FeedAll(batch); err != ErrInvalidLevel {
		t.Fatalf("FeedAll invalid error = %v, want %v", err, ErrInvalidLevel)
	}
	if got := receiver.SampleCount(); got != beforeSamples {
		t.Fatalf("SampleCount changed from %d to %d after rejected operations", beforeSamples, got)
	}
	if _, err := receiver.Pop(); err != ErrQueueEmpty {
		t.Fatalf("Pop empty error = %v, want %v", err, ErrQueueEmpty)
	}
	t.Logf("input contained level=2; output sampleCount=%d and queue length=%d unchanged", receiver.SampleCount(), receiver.Len())
}

func TestEveryFeedSplitMatchesNaiveWaveform(t *testing.T) {
	wave := append([]int{1, 1, 0, 1, 1, 1, 1, 1, 1, 1}, waveformFrame(0x5a, ParityEven, 1)...)
	wave = append(wave, waveformFrameWithParity(0, ParityOdd, 0, 0)...)
	wave = append(wave, 0, 1)
	wave = append(wave, waveformFrame(0xa5, ParityNone, 1)...)

	wantFrames, wantGlitches, wantOverflows := naiveReceive(wave, ParityEven, 4)
	t.Logf("naive input len=%d output frames=%+v glitches=%d overflows=%d", len(wave), wantFrames, wantGlitches, wantOverflows)

	for split := 0; split <= len(wave); split++ {
		receiver, err := NewReceiver(ParityEven, 4)
		if err != nil {
			t.Fatal(err)
		}
		if err := receiver.FeedAll(wave[:split]); err != nil {
			t.Fatal(err)
		}
		for i := split; i < len(wave); i++ {
			if err := receiver.Feed(wave[i]); err != nil {
				t.Fatal(err)
			}
		}

		got := drainFrames(t, receiver)
		if !framesEqual(got, wantFrames) {
			t.Fatalf("split=%d frames=%+v, want %+v", split, got, wantFrames)
		}
		if receiver.GlitchCount() != wantGlitches || receiver.OverflowCount() != wantOverflows {
			t.Fatalf("split=%d glitches=%d/%d overflows=%d/%d", split, receiver.GlitchCount(), wantGlitches, receiver.OverflowCount(), wantOverflows)
		}
		if receiver.SampleCount() != uint64(len(wave)) || receiver.ProducedCount() != uint64(len(wantFrames))+wantOverflows {
			t.Fatalf("split=%d samples=%d produced=%d", split, receiver.SampleCount(), receiver.ProducedCount())
		}
	}
	t.Logf("basis: all %d one-point split points between FeedAll and Feed produced identical results", len(wave)+1)
}

func TestConcurrentFeedsPopsAndQueriesSerialize(t *testing.T) {
	receiver, err := NewReceiver(ParityNone, 128)
	if err != nil {
		t.Fatal(err)
	}
	var waiter sync.WaitGroup

	waiter.Add(3)
	go func() {
		defer waiter.Done()
		for i := 0; i < 32; i++ {
			if err := receiver.FeedAll(waveformFrame(byte(i), ParityNone, 1)); err != nil {
				t.Error(err)
			}
		}
	}()
	go func() {
		defer waiter.Done()
		for i := 0; i < 32; {
			if frame, popErr := receiver.Pop(); popErr == nil {
				if int(frame.Byte) != i {
					t.Errorf("frame byte=%#x, want %#x", frame.Byte, i)
				}
				i++
			}
		}
	}()
	go func() {
		defer waiter.Done()
		for i := 0; i < 1000; i++ {
			_ = receiver.Len()
			_ = receiver.SampleCount()
			_ = receiver.GlitchCount()
			_ = receiver.OverflowCount()
			_ = receiver.ProducedCount()
		}
	}()

	waiter.Wait()
	if receiver.Len() != 0 || receiver.ProducedCount() != 32 || receiver.OverflowCount() != 0 || receiver.SampleCount() != 32*160 {
		t.Fatalf("len=%d produced=%d overflow=%d samples=%d", receiver.Len(), receiver.ProducedCount(), receiver.OverflowCount(), receiver.SampleCount())
	}
	t.Logf("concurrent output 32 FIFO frames; samples=%d produced=%d", receiver.SampleCount(), receiver.ProducedCount())
}

func naiveReceive(levels []int, initialParity ParityMode, depth uint64) ([]Frame, uint64, uint64) {
	state := stateIdle
	start := uint64(0)
	var data byte
	dataIndex := 0
	parityBit := 0
	var frames []Frame
	var glitches uint64
	var overflows uint64

	for index, level := range levels {
		idx := uint64(index)
		switch state {
		case stateIdle:
			if level == 0 {
				start = idx
				data = 0
				dataIndex = 0
				parityBit = 0
				state = stateReceiving
			}
		case stateReceiving:
			offset := int(idx - start)
			if offset < 7 {
				continue
			}
			if offset == 7 {
				if level == 1 {
					glitches++
					state = stateIdle
				}
				continue
			}
			if (offset-7)%16 != 0 {
				continue
			}
			bitNumber := (offset - 7) / 16
			if bitNumber >= 1 && bitNumber <= 8 {
				if level == 1 {
					data |= 1 << (bitNumber - 1)
				}
				dataIndex = bitNumber
			} else if bitNumber == 9 && initialParity != ParityNone {
				parityBit = level
			} else if bitNumber == stopBitNumber(initialParity) {
				parityError := false
				if initialParity != ParityNone {
					ones := onesCount(data) + parityBit
					parityError = initialParity == ParityEven && ones%2 != 0
					parityError = parityError || (initialParity == ParityOdd && ones%2 != 1)
				}
				framing := level == 0
				breakFrame := framing && data == 0 && (initialParity == ParityNone || parityBit == 0)
				frame := Frame{
					Byte:           data,
					ParityError:    parityError,
					FramingError:   framing,
					Break:          breakFrame,
					EndSampleIndex: idx,
				}
				if uint64(len(frames)) >= depth {
					overflows++
				} else {
					frames = append(frames, frame)
				}
				if framing {
					state = stateWaitHigh
				} else {
					state = stateIdle
				}
			}
		case stateWaitHigh:
			if level == 1 {
				state = stateIdle
			}
		}
	}
	_ = dataIndex
	return frames, glitches, overflows
}

func drainFrames(t *testing.T, receiver *Receiver) []Frame {
	t.Helper()
	var frames []Frame
	for {
		frame, err := receiver.Pop()
		if err == ErrQueueEmpty {
			return frames
		}
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, frame)
	}
}

func framesEqual(left, right []Frame) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func waveformFrame(value byte, parity ParityMode, stopLevel int) []int {
	parityBit := 0
	if parity == ParityEven {
		parityBit = onesCount(value) % 2
	} else if parity == ParityOdd {
		parityBit = 1 - onesCount(value)%2
	}
	return waveformFrameWithParity(value, parity, parityBit, stopLevel)
}

func waveformFrameWithParity(value byte, parity ParityMode, parityBit int, stopLevel int) []int {
	levels := make([]int, 0, 160)
	appendBits := func(level int, count int) {
		for i := 0; i < count; i++ {
			levels = append(levels, level)
		}
	}

	appendBits(0, 16)
	for i := 0; i < 8; i++ {
		appendBits(int((value>>i)&1), 16)
	}
	if parity != ParityNone {
		appendBits(parityBit, 16)
	}
	appendBits(stopLevel, 16)
	return levels
}

func onesCount(value byte) int {
	count := 0
	for value != 0 {
		count += int(value & 1)
		value >>= 1
	}
	return count
}
