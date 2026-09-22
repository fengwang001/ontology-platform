package codec

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func encodeOrDie(t *testing.T, payload []byte) []byte {
	t.Helper()
	rec, err := Encode(payload)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return rec
}

// 语义 1：完整 / 半条 / 校验不符三种结果彼此可判定。
func TestDecodeThreeKindsDistinguishable(t *testing.T) {
	rec := encodeOrDie(t, []byte("hello"))

	full := Decode(rec)
	if full.Kind != Complete {
		t.Fatalf("full record: got %v, want Complete", full.Kind)
	}
	if !bytes.Equal(full.Payload, []byte("hello")) || full.Size != len(rec) {
		t.Fatalf("full record: payload=%q size=%d", full.Payload, full.Size)
	}

	half := Decode(rec[:len(rec)-1])
	if half.Kind != Truncated {
		t.Fatalf("cut record: got %v, want Truncated", half.Kind)
	}

	bad := bytes.Clone(rec)
	bad[LenSize] ^= 0xFF // 翻转负载第一个字节
	corrupt := Decode(bad)
	if corrupt.Kind != Corrupt {
		t.Fatalf("flipped record: got %v, want Corrupt", corrupt.Kind)
	}

	if full.Kind == half.Kind || half.Kind == corrupt.Kind || full.Kind == corrupt.Kind {
		t.Fatal("three kinds must be mutually distinguishable")
	}
}

// 语义 5：空缓冲、零长度前缀、超长前缀都可判定且彼此可区分。
func TestDecodeDegenerateInputs(t *testing.T) {
	empty := Decode(nil)
	if empty.Kind != Truncated || empty.Missing != -1 {
		t.Fatalf("empty: got %v missing=%d", empty.Kind, empty.Missing)
	}

	zeroLenPrefix := make([]byte, LenSize) // 只有一个零长度前缀
	zeroOnly := Decode(zeroLenPrefix)
	if zeroOnly.Kind != Truncated || zeroOnly.Missing != SumSize {
		t.Fatalf("zero-len prefix: got %v missing=%d, want Truncated missing=%d",
			zeroOnly.Kind, zeroOnly.Missing, SumSize)
	}

	huge := make([]byte, LenSize)
	binary.BigEndian.PutUint32(huge, 1<<20) // 声称 1MiB，实际没有负载
	far := Decode(huge)
	if far.Kind != Truncated || far.Missing != HeaderSize+(1<<20)-LenSize {
		t.Fatalf("huge prefix: got %v missing=%d", far.Kind, far.Missing)
	}

	insane := make([]byte, LenSize)
	binary.BigEndian.PutUint32(insane, MaxPayload+1) // 超出合法上限
	over := Decode(insane)
	if over.Kind != Corrupt {
		t.Fatalf("over-max prefix: got %v, want Corrupt", over.Kind)
	}

	if zeroOnly.Missing == far.Missing {
		t.Fatal("zero-len prefix and huge prefix must be distinguishable")
	}
}

// 语义 6：零长度负载是合法记录，不能与"没有记录"混淆。
func TestZeroLengthPayloadRoundTrip(t *testing.T) {
	rec := encodeOrDie(t, nil)
	if len(rec) != HeaderSize {
		t.Fatalf("encoded empty payload: len=%d, want %d", len(rec), HeaderSize)
	}
	res := Decode(rec)
	if res.Kind != Complete {
		t.Fatalf("empty payload record: got %v, want Complete", res.Kind)
	}
	if res.Payload == nil || len(res.Payload) != 0 {
		t.Fatalf("empty payload record: payload=%v, want non-nil empty", res.Payload)
	}
	if res.Size != HeaderSize {
		t.Fatalf("empty payload record: size=%d, want %d", res.Size, HeaderSize)
	}
}

// 语义 4：负载中任意单字节翻转都被校验和发现。
func TestSingleByteFlipAlwaysDetected(t *testing.T) {
	payload := []byte("the quick brown fox jumps over the lazy dog")
	rec := encodeOrDie(t, payload)
	for i := LenSize; i < LenSize+len(payload); i++ {
		bad := bytes.Clone(rec)
		bad[i] ^= 0x01
		if got := Decode(bad); got.Kind != Corrupt {
			t.Fatalf("flip at byte %d: got %v, want Corrupt", i, got.Kind)
		}
	}
}

// 往返：多种负载编码后解码结果与原文一致。
func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := [][]byte{
		{},
		{0},
		[]byte("a"),
		bytes.Repeat([]byte{0xAB}, 4096),
	}
	for _, payload := range cases {
		rec := encodeOrDie(t, payload)
		if len(rec) != EncodedLen(len(payload)) {
			t.Fatalf("len=%d: encoded len=%d, want %d",
				len(payload), len(rec), EncodedLen(len(payload)))
		}
		res := Decode(rec)
		if res.Kind != Complete || !bytes.Equal(res.Payload, payload) {
			t.Fatalf("len=%d: kind=%v payload match=%v",
				len(payload), res.Kind, bytes.Equal(res.Payload, payload))
		}
	}
}

// 超限负载在编码侧被拒绝。
func TestEncodeRejectsOversizedPayload(t *testing.T) {
	if _, err := Encode(make([]byte, MaxPayload+1)); err == nil {
		t.Fatal("expected error for oversized payload")
	}
}

// 解码返回的负载是副本，修改它不影响原始缓冲。
func TestDecodeReturnsCopy(t *testing.T) {
	rec := encodeOrDie(t, []byte("abc"))
	res := Decode(rec)
	res.Payload[0] = 'X'
	if rec[LenSize] != 'a' {
		t.Fatal("Decode must return an independent copy of payload")
	}
}
