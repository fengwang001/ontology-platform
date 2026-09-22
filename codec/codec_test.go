package codec

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	payload := []byte("hello wal")
	enc := Encode(payload)
	if len(enc) != EncodedLen(len(payload)) {
		t.Fatalf("encoded len = %d, want %d", len(enc), EncodedLen(len(payload)))
	}
	res := Decode(enc)
	if res.Kind != KindOK {
		t.Fatalf("kind = %v, want KindOK", res.Kind)
	}
	if !bytes.Equal(res.Payload, payload) {
		t.Fatalf("payload = %q, want %q", res.Payload, payload)
	}
	if res.Consumed != len(enc) {
		t.Fatalf("consumed = %d, want %d", res.Consumed, len(enc))
	}
}

func TestDecodeThreeKindsAreDistinguishable(t *testing.T) {
	enc := Encode([]byte("abc"))

	ok := Decode(enc)
	trunc := Decode(enc[:len(enc)-1])
	bad := append([]byte(nil), enc...)
	bad[PrefixLen] ^= 0xFF // 翻转负载第一个字节
	corrupt := Decode(bad)

	if ok.Kind != KindOK || trunc.Kind != KindTruncated || corrupt.Kind != KindCorrupt {
		t.Fatalf("kinds = %v/%v/%v, want OK/Truncated/Corrupt", ok.Kind, trunc.Kind, corrupt.Kind)
	}
	if trunc.Reason == TruncNone {
		t.Fatal("truncated result must carry a reason")
	}
}

func TestDecodeEmptyInput(t *testing.T) {
	res := Decode(nil)
	if res.Kind != KindTruncated || res.Reason != TruncPrefix {
		t.Fatalf("empty input: kind=%v reason=%v, want Truncated/Prefix", res.Kind, res.Reason)
	}
	if res.Need != PrefixLen {
		t.Fatalf("need = %d, want %d", res.Need, PrefixLen)
	}
}

func TestDecodeZeroLengthPrefixOnly(t *testing.T) {
	buf := make([]byte, PrefixLen) // 只有一个零长度前缀
	res := Decode(buf)
	if res.Kind != KindTruncated || res.Reason != TruncChecksum {
		t.Fatalf("kind=%v reason=%v, want Truncated/Checksum", res.Kind, res.Reason)
	}
	if res.Declared != 0 || res.Need != ChecksumLen {
		t.Fatalf("declared=%d need=%d, want 0/%d", res.Declared, res.Need, ChecksumLen)
	}
}

func TestDecodeHugeDeclaredLength(t *testing.T) {
	buf := make([]byte, PrefixLen)
	binary.BigEndian.PutUint32(buf, 1<<30) // 声称 1GiB，远超剩余字节
	res := Decode(buf)
	if res.Kind != KindTruncated || res.Reason != TruncPayload {
		t.Fatalf("kind=%v reason=%v, want Truncated/Payload", res.Kind, res.Reason)
	}
	if res.Declared != 1<<30 {
		t.Fatalf("declared = %d, want %d", res.Declared, 1<<30)
	}
}

func TestTruncReasonsDistinguishable(t *testing.T) {
	onlyPrefix := Decode(make([]byte, PrefixLen))
	huge := Decode(func() []byte {
		b := make([]byte, PrefixLen)
		binary.BigEndian.PutUint32(b, 1<<30)
		return b
	}())
	if onlyPrefix.Reason == huge.Reason {
		t.Fatalf("zero-length prefix and huge length must differ, both %v", onlyPrefix.Reason)
	}
}

func TestDecodeZeroLengthPayload(t *testing.T) {
	res := Decode(Encode(nil))
	if res.Kind != KindOK {
		t.Fatalf("kind = %v, want KindOK", res.Kind)
	}
	if res.Payload == nil || len(res.Payload) != 0 {
		t.Fatalf("payload = %v, want empty non-nil slice", res.Payload)
	}
	if res.Consumed != HeaderLen {
		t.Fatalf("consumed = %d, want %d", res.Consumed, HeaderLen)
	}
}

func TestSingleByteFlipDetected(t *testing.T) {
	payload := []byte("flip me anywhere")
	enc := Encode(payload)
	for i := PrefixLen; i < PrefixLen+len(payload); i++ {
		bad := append([]byte(nil), enc...)
		bad[i] ^= 0x01
		if res := Decode(bad); res.Kind != KindCorrupt {
			t.Fatalf("flip at %d: kind = %v, want KindCorrupt", i, res.Kind)
		}
	}
}

func TestDecodeDoesNotAliasInput(t *testing.T) {
	enc := Encode([]byte("abc"))
	res := Decode(enc)
	enc[PrefixLen] = 'X'
	if string(res.Payload) != "abc" {
		t.Fatalf("payload aliases input: %q", res.Payload)
	}
}
