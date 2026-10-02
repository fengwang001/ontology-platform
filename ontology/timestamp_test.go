package ontology

import (
	"errors"
	"math"
	"sync"
	"testing"
)

func TestTimestampDODBoundaries(t *testing.T) {
	cases := []struct {
		name string
		dods []int64
	}{
		{"seven-bit-positive", []int64{64, 65, -63, -64}},
		{"nine-bit-positive", []int64{256, 257, -255, -256}},
		{"twelve-bit-positive", []int64{2048, 2049, -2047}},
		{"thirty-two-bit-positive-end", []int64{1<<31 - 1}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const firstDelta int64 = 100
			block := newTimestampsBlock(t, firstDelta, tc.dods)
			timestamps := timestampsForDODs(10000, firstDelta, tc.dods)

			if got := block.Len(); got != 1+len(tc.dods) {
				t.Fatalf("Len = %d, want %d", got, 1+len(tc.dods))
			}
			if err := block.Seal(); err != nil {
				t.Fatalf("Seal: %v", err)
			}
			start, samples, err := Decode(block.Bytes())
			if err != nil {
				t.Fatalf("Decode: %v; input dods=%v bits=%d bytes=%08b", err, tc.dods, block.Bits(), block.Bytes())
			}
			if start != 10000 || len(samples) != len(timestamps) {
				t.Fatalf("decoded start=%d count=%d, want 10000 and %d", start, len(samples), len(timestamps))
			}
			for i, timestamp := range timestamps {
				if samples[i].Timestamp != timestamp {
					t.Fatalf("decoded timestamp[%d]=%d, want %d; dod input=%v", i, samples[i].Timestamp, timestamp, tc.dods)
				}
			}
			t.Logf("input=%s dods=%v output timestamps=%v bits=%d; 判定依据：逐档边界往返匹配", tc.name, tc.dods, timestamps, block.Bits())
		})
	}
}

func TestThirtyTwoBitNegativeEndBitEncoding(t *testing.T) {
	var block Block
	block.writeTimestampDOD(-1 << 31)
	assertEncodedDOD(t, &block, -1<<31, 0b1000_0000_0000_0000_0000_0000_0000_0000)
}

func TestTwelveBitOuterValueUsesThirtyTwoBitEncoding(t *testing.T) {
	var block Block
	block.writeTimestampDOD(-2048)
	assertEncodedDOD(t, &block, -2048, 0xFFFF_F800)
}

func assertEncodedDOD(t *testing.T, block *Block, want int64, payload uint64) {
	t.Helper()
	reader := bitReader{data: append([]byte(nil), block.data...)}
	prefix, ok := reader.read(4)
	if !ok || prefix != 0b1111 {
		t.Fatalf("prefix=%04b ok=%v, want 1111", prefix, ok)
	}
	got, ok := reader.read(32)
	if !ok || got != payload {
		t.Fatalf("encoded payload=%d ok=%v, want %d", got, ok, payload)
	}
	if block.bits != 36 {
		t.Fatalf("bits=%d, want 36", block.bits)
	}
	decoded := int64(int32(uint32(payload)))
	if decoded != want {
		t.Fatalf("int32 payload decoded=%d, want %d", decoded, want)
	}
	t.Logf("input dod=%d output prefix=1111 payload=%032b; 判定依据：32 位按 int32 解释", want, payload)
}

func TestTimestampBitsBoundarySizes(t *testing.T) {
	cases := []struct {
		dod      int64
		wantBits int
	}{
		{0, 1}, {64, 9}, {65, 12}, {-63, 9}, {-64, 12},
		{256, 12}, {257, 16}, {-255, 12}, {-256, 16},
		{2048, 16}, {2049, 36}, {-2047, 16}, {-2048, 36},
		{1<<31 - 1, 36}, {-1 << 31, 36},
	}
	for _, tc := range cases {
		if got := timestampBits(tc.dod); got != tc.wantBits {
			t.Fatalf("timestampBits(%d)=%d, want %d", tc.dod, got, tc.wantBits)
		}
	}
}

func TestFirstDeltaBoundaries(t *testing.T) {
	for _, d0 := range []int64{0, 15359} {
		block, err := New(1000, 64)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if err := block.Append(1000+d0, 1); err != nil {
			t.Fatalf("Append d0=%d: %v", d0, err)
		}
		t.Logf("input d0=%d output bits=%d; 判定依据：合法首差分应追加成功", d0, block.Bits())
	}

	block, err := New(1000, 64)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, timestamp := range []int64{999, 25360} {
		if err := block.Append(timestamp, 1); !errors.Is(err, ErrDelta) {
			t.Fatalf("Append timestamp=%d error=%v, want ErrDelta", timestamp, err)
		}
	}
	if block.Len() != 0 || block.Bits() != 64 {
		t.Fatalf("rejected first appends changed state: len=%d bits=%d", block.Len(), block.Bits())
	}
	t.Logf("input timestamps=999,25360 output len=%d bits=%d; 判定依据：d0 越界和小于起点均为 ErrDelta 且原子拒绝", block.Len(), block.Bits())
}

func TestErrorOrderAndSealedPrecedence(t *testing.T) {
	block, err := New(1000, 64)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := block.Append(1010, 1); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := block.Append(1009, 1); !errors.Is(err, ErrOrder) {
		t.Fatalf("backward Append error=%v, want ErrOrder", err)
	}
	if err := block.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	for _, run := range []func() error{
		func() error { return block.Append(1, 1) },
		func() error { return block.Seal() },
	} {
		if err := run(); !errors.Is(err, ErrSealed) {
			t.Fatalf("sealed operation error=%v, want ErrSealed", err)
		}
	}
	t.Log("input=backward append then append/seal after sealed; output=ErrOrder then ErrSealed; 判定依据：错误次序")
}

func TestExactCapacityBoundary(t *testing.T) {
	exactBytes := (142 + 36 + 7) / 8
	block, err := New(1000, exactBytes)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := block.Append(1010, 12); err != nil {
		t.Fatalf("exact-capacity Append: %v", err)
	}
	if err := block.Seal(); err != nil {
		t.Fatalf("exact-capacity Seal: %v", err)
	}

	small, err := New(1000, exactBytes-1)
	if err != nil {
		t.Fatalf("New small: %v", err)
	}
	if err := small.Append(1010, 12); !errors.Is(err, ErrFull) {
		t.Fatalf("one-bit-smaller Append error=%v, want ErrFull", err)
	}
	if small.Len() != 0 || small.Bits() != 64 {
		t.Fatalf("rejected append changed state: len=%d bits=%d", small.Len(), small.Bits())
	}
	t.Logf("input maxBytes=%d/%d output accepted/rejected; 判定依据：178 位恰等，少一位拒绝", exactBytes, exactBytes-1)
}

func TestRejectedAppendIsAtomicWithIndependentBlock(t *testing.T) {
	makeBlock := func() *Block {
		block, err := New(1000, 24)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		mustAppend(t, block, 1010, 12)
		mustAppend(t, block, 1020, 12)
		return block
	}

	rejected := makeBlock()
	clean := makeBlock()
	if err := rejected.Append(1030, 24); !errors.Is(err, ErrFull) {
		t.Fatalf("rejected Append: %v", err)
	}
	mustAppend(t, clean, 1030, 12)

	if rejected.Bits() != 144 || clean.Bits() != 146 {
		t.Fatalf("bits rejected=%d clean=%d", rejected.Bits(), clean.Bits())
	}
	rejectedBits := rejected.Bits()
	if err := rejected.Seal(); err != nil {
		t.Fatalf("Seal rejected-history block: %v", err)
	}
	if err := clean.Seal(); err != nil {
		t.Fatalf("Seal clean block: %v", err)
	}
	cleanBits := clean.Bits()
	if rejected.Bits() != rejectedBits+36 || clean.Bits() != cleanBits {
		t.Fatalf("seal bits rejected=%d clean=%d", rejected.Bits(), clean.Bits())
	}
}

func TestConcurrentAppendAccessorsAndSeal(t *testing.T) {
	const workers = 16
	const perWorker = 50
	block, err := New(0, 1048576)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perWorker {
				value := math.Float64frombits(1<<10 | uint64(worker*perWorker))
				if err := block.Append(1, value); err != nil {
					t.Errorf("Append duplicate timestamp: %v", err)
					return
				}
				_ = block.Bits()
				_ = block.Len()
				_ = block.Bytes()
			}
		}()
	}
	wg.Wait()

	if err := block.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	start, samples, err := Decode(block.Bytes())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if start != 0 || len(samples) != workers*perWorker {
		t.Fatalf("start=%d count=%d, want 0 and %d", start, len(samples), workers*perWorker)
	}
	for _, sample := range samples {
		if sample.Timestamp != 1 || sample.Value == 0 {
			t.Fatalf("invalid decoded sample %+v", sample)
		}
	}
	t.Logf("input=%d goroutines x %d appends output=%d samples bits=%d; 判定依据：并发调用可串行化且解码无丢失重复", workers, perWorker, len(samples), block.Bits())
}

func newTimestampsBlock(t *testing.T, firstDelta int64, dods []int64) *Block {
	t.Helper()
	block, err := New(10000, 1048576)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i, timestamp := range timestampsForDODs(10000, firstDelta, dods) {
		if err := block.Append(timestamp, float64(i)); err != nil {
			dod := firstDelta
			if i > 0 {
				dod = dods[i-1]
			}
			t.Fatalf("Append[%d] timestamp=%d dod=%d: %v", i, timestamp, dod, err)
		}
	}
	return block
}

func timestampsForDODs(start, firstDelta int64, dods []int64) []int64 {
	timestamps := make([]int64, 1+len(dods))
	current := start
	previousDelta := firstDelta
	current += previousDelta
	timestamps[0] = current
	for i, dod := range dods {
		previousDelta += dod
		current += previousDelta
		timestamps[i+1] = current
	}
	return timestamps
}

func mustAppend(t *testing.T, block *Block, timestamp int64, value float64) {
	t.Helper()
	if err := block.Append(timestamp, value); err != nil {
		t.Fatalf("Append(%d,%g): %v", timestamp, value, err)
	}
}
