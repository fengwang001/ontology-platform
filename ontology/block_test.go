package ontology

import (
	"errors"
	"math"
	"testing"
)

func TestSpecifiedExample(t *testing.T) {
	block, err := New(1000, 64)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	appends := []struct {
		timestamp int64
		value     float64
		wantBits  int
	}{
		{1010, 12.0, 142},
		{1020, 12.0, 144},
		{1030, 24.0, 159},
		{1031, 24.0, 169},
		{1032, 28.0, 184},
	}

	for index, sample := range appends {
		if err := block.Append(sample.timestamp, sample.value); err != nil {
			t.Fatalf("Append[%d](%d, %g): %v", index, sample.timestamp, sample.value, err)
		}
		if got := block.Bits(); got != sample.wantBits {
			t.Fatalf("Bits after Append[%d] = %d, want %d; input=%+v", index, got, sample.wantBits, sample)
		}
	}

	if err := block.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if got := block.Bits(); got != 220 {
		t.Fatalf("Bits after Seal = %d, want 220", got)
	}
	if got := len(block.Bytes()); got != 28 {
		t.Fatalf("len(Bytes) = %d, want 28", got)
	}

	start, samples, err := Decode(block.Bytes())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if start != 1000 || len(samples) != len(appends) {
		t.Fatalf("Decode start=%d count=%d, want 1000 and %d", start, len(samples), len(appends))
	}
	for index, sample := range appends {
		if samples[index].Timestamp != sample.timestamp ||
			math.Float64bits(samples[index].Value) != math.Float64bits(sample.value) {
			t.Fatalf("decoded[%d]=%+v, want timestamp=%d value=%g", index, samples[index], sample.timestamp, sample.value)
		}
	}
	t.Logf("input=%v output start=%d samples=%v bits=%d bytes=%d; 判定依据：逐次追加位长、封口位长与解码往返", appends, start, samples, block.Bits(), len(block.Bytes()))
}

func TestCapacityExample(t *testing.T) {
	block, err := New(1000, 24)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := block.Append(1010, 12.0); err != nil {
		t.Fatalf("first Append: %v", err)
	}
	if err := block.Append(1020, 12.0); err != nil {
		t.Fatalf("second Append: %v", err)
	}

	before := block.Bytes()
	err = block.Append(1030, 24.0)
	if !errors.Is(err, ErrFull) {
		t.Fatalf("third Append error = %v, want ErrFull", err)
	}
	if got := block.Bits(); got != 144 {
		t.Fatalf("Bits after rejected Append = %d, want 144", got)
	}
	assertBytes(t, block.Bytes(), before)

	if err := block.Append(1030, 12.0); err != nil {
		t.Fatalf("repeated-timestamp Append after rejection: %v", err)
	}
	if got := block.Bits(); got != 146 {
		t.Fatalf("Bits after repeated-timestamp Append = %d, want 146", got)
	}
	t.Logf("input rejected=(1030,24) then accepted=(1030,12); output bits=%d; 判定依据：拒绝前后字节相同且 dod 仍以 0 追加", block.Bits())
}

func assertBytes(t *testing.T, got, want []byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("byte length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte[%d] = %08b, want %08b", i, got[i], want[i])
		}
	}
}

func assertBitPrefix(t *testing.T, data []byte, bits int, want []byte) {
	t.Helper()
	wholeBytes := bits / 8
	assertBytes(t, data[:wholeBytes], want[:wholeBytes])
	remainder := bits % 8
	if remainder == 0 {
		return
	}
	mask := byte(0xFF << (8 - remainder))
	if got, want := data[wholeBytes]&mask, want[wholeBytes]&mask; got != want {
		t.Fatalf("partial byte after %d bits = %08b, want %08b", bits, got, want)
	}
}
