package ontology

import (
	"bytes"
	"hash/crc32"
	"testing"
)

func TestRLESpecVectors(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want []byte
	}{
		{"70 a", bytes.Repeat([]byte{'a'}, 70), []byte{0xC3, 'a'}},
		{"130 a", bytes.Repeat([]byte{'a'}, 130), []byte{0xFF, 'a'}},
		{"131 a", bytes.Repeat([]byte{'a'}, 131), []byte{0xFF, 'a', 0x00, 'a'}},
		{"132 a", bytes.Repeat([]byte{'a'}, 132), []byte{0xFF, 'a', 0x01, 'a', 'a'}},
		{"133 a", bytes.Repeat([]byte{'a'}, 133), []byte{0xFF, 'a', 0x80, 'a'}},
		{"260 a", bytes.Repeat([]byte{'a'}, 260), []byte{0xFF, 'a', 0xFF, 'a'}},
		{"261 a", bytes.Repeat([]byte{'a'}, 261), []byte{0xFF, 'a', 0xFF, 'a', 0x00, 'a'}},
		{"short run then run", []byte{'a', 'a', 'b', 'b', 'b'}, []byte{0x01, 'a', 'a', 0x80, 'b'}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rleEncode(tc.in)
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("encode %s: got % x, want % x", tc.name, got, tc.want)
			}
			dec, err := rleDecode(got, maxChunkData)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !bytes.Equal(dec, tc.in) {
				t.Fatalf("round trip mismatch: got %d bytes want %d", len(dec), len(tc.in))
			}
		})
	}
}

func TestRLELiteralBuffer128(t *testing.T) {
	in := make([]byte, 128)
	for i := range in {
		in[i] = byte(i) // 相邻字节均不同
	}
	got := rleEncode(in)
	if len(got) != 129 || got[0] != 127 {
		t.Fatalf("exact 128 literals: got % x (len=%d)", got[:min(8, len(got))], len(got))
	}
	if !bytes.Equal(got[1:], in) {
		t.Fatal("literal payload mismatch")
	}
	// 129 个全不同字节：128 字面值令牌 + 1 字面值令牌
	in = make([]byte, 129)
	for i := range in {
		in[i] = byte(i * 3) // 保证与相邻不同（回绕也基本满足，遇相同再处理）
	}
	// 若构造出意外的 3 连同值则跳过该断言，单独用保证互异序列
	distinct := make([]byte, 129)
	for i := range distinct {
		distinct[i] = byte((i * 31) % 251) // 251 为质数，129<251 保证互异
	}
	got = rleEncode(distinct)
	if len(got) != 131 || got[0] != 127 || got[129] != 0 {
		t.Fatalf("129 literals split: len=%d head=%x tail=%x", len(got), got[0], got[129])
	}
	dec, err := rleDecode(got, maxChunkData)
	if err != nil || !bytes.Equal(dec, distinct) {
		t.Fatalf("round trip: err=%v", err)
	}
}

func TestRLEDecodeErrors(t *testing.T) {
	// 字面值令牌被截断
	if _, err := rleDecode([]byte{0x05, 1, 2}, maxChunkData); err != ErrDecode {
		t.Fatalf("truncated literal: %v", err)
	}
	// 重复令牌缺字节
	if _, err := rleDecode([]byte{0x80}, maxChunkData); err != ErrDecode {
		t.Fatalf("truncated run: %v", err)
	}
	// 解出超限：limit=2 时一个 3 次重复即超限
	if _, err := rleDecode([]byte{0x80, 'a'}, 2); err != ErrDecode {
		t.Fatalf("over limit: %v", err)
	}
	// 空负载合法
	if dec, err := rleDecode(nil, maxChunkData); err != nil || len(dec) != 0 {
		t.Fatalf("empty: %v %d", err, len(dec))
	}
}

func TestMaskedChecksumKnown(t *testing.T) {
	for _, p := range [][]byte{nil, {}, []byte("sNaPpY"), bytes.Repeat([]byte{0xAB}, 1000)} {
		raw := crc32.Checksum(p, crcTable)
		want := (raw>>15 | raw<<17) + 0xa282ead8
		if got := maskedChecksum(p); got != want {
			t.Fatalf("mask formula mismatch for len %d: got %08x want %08x", len(p), got, want)
		}
	}
}
