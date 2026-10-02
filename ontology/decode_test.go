package ontology

import (
	"errors"
	"math"
	"testing"
)

func TestParameterBoundaries(t *testing.T) {
	for _, maxBytes := range []int{12, 13, 1048576, 1048577} {
		block, err := New(0, maxBytes)
		valid := maxBytes == 13 || maxBytes == 1048576
		if valid {
			if err != nil || block == nil {
				t.Fatalf("New(0,%d) block=%v err=%v, want valid block", maxBytes, block, err)
			}
		} else if !errors.Is(err, ErrParam) || block != nil {
			t.Fatalf("New(0,%d) block=%v err=%v, want ErrParam and nil", maxBytes, block, err)
		}
		t.Logf("input maxBytes=%d output err=%v; 判定依据：13 与 1048576 合法，相邻越界 ErrParam", maxBytes, err)
	}
}

func TestDecodeAllTruncationPointsAndPadding(t *testing.T) {
	block, err := New(777, 64)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustAppend(t, block, 777, 12.5)
	mustAppend(t, block, 790, math.Float64frombits(0x0123_4567_89ab_cdef))
	mustAppend(t, block, 790, math.Float64frombits(0xfedc_ba98_7654_3210))
	if err := block.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	data := block.Bytes()
	for length := 0; length < len(data); length++ {
		if _, _, err := Decode(data[:length]); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("Decode truncated to %d bytes returned %v, want ErrCorrupt; 判定依据：截断点位不足或无结束标记", length, err)
		}
	}

	paddingBits := len(data)*8 - block.Bits()
	if paddingBits == 0 || paddingBits >= 8 {
		t.Fatalf("paddingBits=%d, want 1..7", paddingBits)
	}
	bad := append([]byte(nil), data...)
	bad[len(bad)-1] |= 1
	if _, _, err := Decode(bad); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("non-zero padding returned %v, want ErrCorrupt", err)
	}

	if _, samples, err := Decode(data); err != nil || len(samples) != 3 {
		t.Fatalf("valid Decode samples=%d err=%v", len(samples), err)
	}
	t.Logf("input bits=%d bytes=%d output truncation checks=0..%d and nonzero padding rejected; 判定依据：标记与补位完整校验", block.Bits(), len(data), len(data)-1)
}

func TestTimestampOverflowAndOrderErrors(t *testing.T) {
	block, err := New(math.MaxInt64-2, 64)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := block.Append(math.MaxInt64, 1); err != nil {
		t.Fatalf("first Append: %v", err)
	}
	if err := block.Append(math.MaxInt64, 2); err != nil {
		t.Fatalf("equal-timestamp Append: %v", err)
	}
	if err := block.Append(math.MaxInt64, 3); err != nil {
		t.Fatalf("repeated delta overflow Append: %v", err)
	}
	before := block.Bits()
	if err := block.Append(math.MaxInt64-1, 4); !errors.Is(err, ErrOrder) {
		t.Fatalf("backward timestamp err=%v, want ErrOrder", err)
	}
	if block.Bits() != before || block.Len() != 3 {
		t.Fatalf("ErrOrder changed state: bits=%d len=%d", block.Bits(), block.Len())
	}
	t.Log("input MaxInt64 repeated timestamps then one backward timestamp; output repeated accepted, backward ErrOrder; 判定依据：错误次序与原子拒绝")
}
