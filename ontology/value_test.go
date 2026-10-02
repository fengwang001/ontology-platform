package ontology

import (
	"errors"
	"math"
	"testing"
)

func TestFloatBitPatternsAndZeroXorWindow(t *testing.T) {
	values := []float64{
		math.Float64frombits(0),
		math.Float64frombits(1 << 63),
		math.Float64frombits(0x7ff8_0000_0000_0001),
		math.Float64frombits(0x7ffc_0000_0000_0001),
	}

	block, err := New(100, 64)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, value := range values {
		if err := block.Append(100, value); err != nil {
			t.Fatalf("Append value bits=%016x: %v", math.Float64bits(value), err)
		}
	}
	for _, value := range values {
		if err := block.Append(100, value); err != nil {
			t.Fatalf("Append repeated value bits=%016x: %v", math.Float64bits(value), err)
		}
	}
	if err := block.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	_, samples, err := Decode(block.Bytes())
	if err != nil {
		t.Fatalf("Decode: %v; bits=%d bytes=%08b", err, block.Bits(), block.Bytes())
	}
	expected := append(append([]float64(nil), values...), values...)
	for index, value := range expected {
		if math.Float64bits(samples[index].Value) != math.Float64bits(value) {
			t.Fatalf("sample[%d] bits=%016x, want %016x", index, math.Float64bits(samples[index].Value), math.Float64bits(value))
		}
	}
	t.Logf("input bits=%016x,%016x,... output=%d samples bits=%d; 判定依据：±0 与不同 NaN 位模式保留，重复值走 0 且不改窗口",
		math.Float64bits(values[0]), math.Float64bits(values[1]), len(samples), block.Bits())
}

func TestEffectiveLength64AndLZCap31(t *testing.T) {
	previous := uint64(0)
	fullXOR := uint64(0x8000_0000_0000_0001)
	if got := valueBitsToAppend(previous, previous^fullXOR, false, 0, 0); got != 77 {
		t.Fatalf("full-length xor cost=%d, want 77", got)
	}

	block := newSealedValueBlock(t, []uint64{previous, previous ^ fullXOR, previous})
	_, samples, err := Decode(block.Bytes())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(samples) != 3 || math.Float64bits(samples[1].Value) != fullXOR {
		t.Fatalf("samples=%+v, want second bits %016x", samples, fullXOR)
	}

	xor := uint64(0x0000_0000_0000_0001)
	if got := valueBitsToAppend(0, xor, false, 0, 0); got != 46 {
		t.Fatalf("lz-capped xor cost=%d, want 46", got)
	}
	t.Log("input full-xor and >31-lz xor; output lengths=77 and 46; 判定依据：长度64写6位0，lz截为31，零xor不改窗口")
}

func TestWindowReuseThreshold(t *testing.T) {
	window := struct {
		lz, tz int
	}{lz: 20, tz: 20}

	reuseXOR := uint64(0)
	reuseXOR |= 1 << uint(window.tz)
	reuseXOR |= 1 << uint(63-window.lz)
	reuseCost := 2 + 64 - window.lz - window.tz
	openLZ := 21
	openTZ := 31
	openCost := 13 + 64 - openLZ - openTZ
	if openCost-reuseCost != -1 {
		t.Fatalf("test setup openCost-reuseCost=%d, want -1", openCost-reuseCost)
	}

	openXOR := uint64(0)
	openXOR |= 1 << uint(openTZ)
	openXOR |= 1 << uint(63-openLZ)
	openValue := reuseXOR&^(1<<uint(window.tz)) | openXOR
	if got := valueBitsToAppend(0, reuseXOR, true, window.lz, window.tz); got != reuseCost {
		t.Fatalf("equal cost encoding returned %d, want reuse %d", got, reuseCost)
	}
	if got := valueBitsToAppend(0, openXOR, true, window.lz, window.tz); got != openCost {
		t.Fatalf("strictly shorter encoding returned %d, want open %d", got, openCost)
	}

	block, err := New(100, 64)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustAppend(t, block, 100, math.Float64frombits(0))
	mustAppend(t, block, 100, math.Float64frombits(reuseXOR))
	bitsBefore := block.Bits()
	mustAppend(t, block, 100, math.Float64frombits(reuseXOR))
	if got := block.Bits() - bitsBefore; got != 2 {
		t.Fatalf("repeated sample bits=%d, want dod 1 plus xor 1", got)
	}

	openBlock, err := New(100, 64)
	if err != nil {
		t.Fatalf("New openBlock: %v", err)
	}
	mustAppend(t, openBlock, 100, math.Float64frombits(0))
	mustAppend(t, openBlock, 100, math.Float64frombits(reuseXOR))
	mustAppend(t, openBlock, 100, math.Float64frombits(openValue))
	block = openBlock
	if err := block.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	_, samples, err := Decode(block.Bytes())
	if err != nil || len(samples) != 3 {
		t.Fatalf("Decode count=%d err=%v", len(samples), err)
	}
	if math.Float64bits(samples[2].Value) != openValue {
		t.Fatalf("last value bits=%016x, want %016x", math.Float64bits(samples[2].Value), openValue)
	}
	t.Logf("input window=(%d,%d), reuseXOR=(%d,%d), openXOR=(%d,%d); output costs=%d/%d; 判定依据：相等复用，重开严格短1位才重开",
		window.lz, window.tz, window.lz, window.tz, openLZ, openTZ, reuseCost, openCost)
}

func TestZeroSampleSealAndTruncationPadding(t *testing.T) {
	block, err := New(12345, 13)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := block.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if block.Bits() != 100 || len(block.Bytes()) != 13 {
		t.Fatalf("bits=%d bytes=%d, want 100 and 13", block.Bits(), len(block.Bytes()))
	}
	start, samples, err := Decode(block.Bytes())
	if err != nil || start != 12345 || len(samples) != 0 {
		t.Fatalf("Decode start=%d samples=%v err=%v", start, samples, err)
	}

	data := block.Bytes()
	for length := 0; length < len(data); length++ {
		if _, _, err := Decode(data[:length]); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("Decode(%d bytes)=%v, want ErrCorrupt; 判定依据：截断点位不足", length, err)
		}
	}

	nonZeroPadding := append([]byte(nil), data...)
	nonZeroPadding[len(nonZeroPadding)-1] |= 1
	if _, _, err := Decode(nonZeroPadding); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("non-zero padding Decode=%v, want ErrCorrupt", err)
	}
	t.Logf("input zero samples output bits=%d; every truncation 0..%d and nonzero trailing padding rejected; 判定依据：无标记或补位非零", block.Bits(), len(data)-1)
}

func newSealedValueBlock(t *testing.T, values []uint64) *Block {
	t.Helper()
	block, err := New(100, 64)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for index, value := range values {
		if err := block.Append(100+int64(index), math.Float64frombits(value)); err != nil {
			t.Fatalf("Append[%d] %016x: %v", index, value, err)
		}
	}
	if err := block.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return block
}
