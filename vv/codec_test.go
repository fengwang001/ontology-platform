package vv

import (
	"errors"
	"testing"
)

func TestVectorRoundTrip(t *testing.T) {
	cases := []Vector{
		{},
		{"A": 1},
		{"A": 1, "B": 0, "C": 42},
		{"replica-x": ^uint64(0), "y": 7},
	}
	for i, v := range cases {
		got, err := DecodeVector(marshalVector(v))
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		// 规范化：编码不含 0 分量，解码后与「缺失即 0」的原向量必须相等。
		if Compare(got, v) != RelEqual {
			t.Fatalf("case %d: got %v want %v", i, got, v)
		}
	}
}

func TestVectorTruncationClasses(t *testing.T) {
	full := marshalVector(Vector{"A": 1, "BB": 2})
	saw := map[error]bool{}
	// 逐字节截断：每个截断点必须落入三类之一或完整成功。
	for n := 0; n < len(full); n++ {
		_, err := DecodeVector(full[:n])
		switch {
		case errors.Is(err, ErrHeaderTruncated):
			saw[ErrHeaderTruncated] = true
		case errors.Is(err, ErrEntryTruncated):
			saw[ErrEntryTruncated] = true
		case errors.Is(err, ErrCRCTruncated):
			saw[ErrCRCTruncated] = true
		default:
			t.Fatalf("len %d: unclassified err %v", n, err)
		}
	}
	for _, e := range []error{ErrHeaderTruncated, ErrEntryTruncated, ErrCRCTruncated} {
		if !saw[e] {
			t.Fatalf("class %v never observed", e)
		}
	}
	if _, err := DecodeVector(full); err != nil {
		t.Fatalf("full decode: %v", err)
	}
	// CRC 完整但内容损坏 → ErrCRCMismatch。
	bad := append([]byte(nil), full...)
	bad[len(bad)-5] ^= 0xFF
	if _, err := DecodeVector(bad); !errors.Is(err, ErrCRCMismatch) {
		t.Fatalf("corrupt = %v", err)
	}
}

func TestFaultSentinelsDistinct(t *testing.T) {
	errs := []error{
		ErrOverflow, ErrUnknownReplica, ErrCounterRollback, ErrPruneUnsafe,
		ErrHeaderTruncated, ErrEntryTruncated, ErrCRCTruncated, ErrCRCMismatch,
	}
	for i, a := range errs {
		for j, b := range errs {
			if i != j && errors.Is(a, b) {
				t.Fatalf("%v and %v not distinguishable", a, b)
			}
		}
	}
}
