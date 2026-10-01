package recordlog

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"
)

// classifyCounts tallies read outcomes by sentinel cause.
type classifyCounts struct {
	length, checksum, sequence, truncated, eof int
}

func classify(err error) string {
	switch {
	case err == io.EOF:
		return "eof"
	case errors.Is(err, ErrLengthOutOfBlock):
		return "length"
	case errors.Is(err, ErrChecksum):
		return "checksum"
	case errors.Is(err, ErrTypeSequence):
		return "sequence"
	case errors.Is(err, ErrTruncated):
		return "truncated"
	default:
		return "other"
	}
}

// compareOutcomes drains both real and naive readers over the same
// bytes and asserts identical records, offsets and error causes.
func compareOutcomes(t *testing.T, data []byte, B int, label string) {
	t.Helper()
	real, _ := NewReader(bytes.NewReader(data), B)
	got := drainReader(t, real)
	want := drainNaiveReader(data, B)
	if !sameResults(got, want) {
		t.Fatalf("%s: outcome mismatch\n got:\n%s\nwant:\n%s",
			label, formatResults(got), formatResults(want))
	}
}

// buildCorpus writes a varied record sequence spanning several blocks,
// including empties and records that force padding.
func buildCorpus(t *testing.T, B int) []byte {
	nb := newNaiveWriter(B)
	lens := []int{0, 1, 0, B - 8, B - 7, 2, B - 1, 50, 0, 30, 7, 100, 0, B - 2}
	for i, n := range lens {
		nb.appendRecord(bytesPattern(300+i, n))
	}
	// Cross-check the corpus itself round-trips.
	r, _ := NewReader(bytes.NewReader(nb.buf), B)
	res := drainReader(t, r)
	if res[len(res)-1].err != io.EOF {
		t.Fatalf("corpus not clean: %v", res[len(res)-1].err)
	}
	return nb.buf
}

// TestFlipEveryByte flips each bit of every byte in the corpus.
func TestFlipEveryByte(t *testing.T) {
	const B = 32
	corpus := buildCorpus(t, B)
	t.Logf("corpus length=%d, blocks=%d", len(corpus), (len(corpus)+B-1)/B)

	counts := classifyCounts{}
	for pos := 0; pos < len(corpus); pos++ {
		for bit := uint(0); bit < 8; bit++ {
			data := bytes.Clone(corpus)
			data[pos] ^= 1 << bit

			compareOutcomes(t, data, B, fmt.Sprintf("flip pos=%d bit=%d", pos, bit))

			r, _ := NewReader(bytes.NewReader(data), B)
			res := drainReader(t, r)
			// no panic is the primary requirement; tally causes too.
			for _, x := range res {
				switch classify(x.err) {
				case "length":
					counts.length++
				case "checksum":
					counts.checksum++
				case "sequence":
					counts.sequence++
				case "truncated":
					counts.truncated++
				case "eof":
					counts.eof++
				}
				if x.err != nil && x.err != io.EOF &&
					!errors.Is(x.err, ErrLengthOutOfBlock) &&
					!errors.Is(x.err, ErrChecksum) &&
					!errors.Is(x.err, ErrTypeSequence) &&
					!errors.Is(x.err, ErrTruncated) {
					t.Fatalf("non-distinguishable error at pos=%d bit=%d: %v", pos, bit, x.err)
				}
			}
			_ = counts
		}
	}
	t.Logf("输入: corpus 每字节 8 种翻转 (%d 次)；输出类别计数: length=%d checksum=%d sequence=%d truncated=%d eof=%d；判定依据: 头长度越界→截断→校验→类型顺序",
		len(corpus)*8, counts.length, counts.checksum, counts.sequence, counts.truncated, counts.eof)
}

// TestTruncateEveryPosition truncates the stream at every byte length.
func TestTruncateEveryPosition(t *testing.T) {
	const B = 32
	corpus := buildCorpus(t, B)
	counts := classifyCounts{}
	for n := 0; n <= len(corpus); n++ {
		data := corpus[:n]
		compareOutcomes(t, data, B, fmt.Sprintf("truncate n=%d", n))

		r, _ := NewReader(bytes.NewReader(data), B)
		res := drainReader(t, r)
		last := res[len(res)-1]
		if last.err != io.EOF {
			t.Fatalf("n=%d: terminal outcome must be EOF, got %v", n, last.err)
		}
		for _, x := range res {
			switch classify(x.err) {
			case "truncated":
				counts.truncated++
				// After truncation the next call is EOF.
			case "eof":
				counts.eof++
			case "length":
				counts.length++
			case "checksum":
				counts.checksum++
			case "sequence":
				counts.sequence++
			}
		}
	}
	t.Logf("输入: 逐位置截断 0..%d；输出: length=%d checksum=%d sequence=%d truncated=%d eof=%d；判定依据: 截断后下一次必 EOF",
		len(corpus), counts.length, counts.checksum, counts.sequence, counts.truncated, counts.eof)
}

// TestCorruptionRecoveryPosition verifies that after a non-truncation
// corruption, reading resumes exactly at the next block boundary.
func TestCorruptionRecoveryPosition(t *testing.T) {
	const B = 32
	corpus := buildCorpus(t, B)

	// Corrupt the CRC of the first fragment (byte 0 of the corpus).
	// If that first fragment occupies block 0, the record there is
	// discarded and the next valid record begins in block 1.
	for _, pos := range []int{0, B + 1, 2 * B} {
		if pos >= len(corpus) {
			continue
		}
		data := bytes.Clone(corpus)
		data[pos] ^= 0xFF
		r, _ := NewReader(bytes.NewReader(data), B)
		_, off, err := r.Next()
		if err == nil {
			continue // flip may have hit padding
		}
		var ce *CorruptError
		if !errors.As(err, &ce) {
			t.Fatalf("pos=%d err %T not CorruptError", pos, err)
		}
		// Error offset must be the offending fragment header start.
		if ce.Offset != off {
			t.Fatalf("returned offset %d != corrupt offset %d", off, ce.Offset)
		}
		// Resume point = next block after the offending fragment's block.
		nextBoundary := (ce.Offset/int64(B) + 1) * int64(B)
		rec2, off2, err2 := r.Next()
		if err2 == nil {
			if off2 < nextBoundary {
				t.Fatalf("resumed at %d before boundary %d (recLen=%d)", off2, nextBoundary, len(rec2))
			}
		}
		t.Logf("输入: 翻转位置 %d；输出: 错误偏移=%d 类别=%v，恢复偏移=%d；判定依据: 丢弃记录并跳到下一块边界",
			pos, ce.Offset, ce.Err, off2)
	}
}

// TestTypeSequenceIllegal crafts illegal type sequences directly.
func TestTypeSequenceIllegal(t *testing.T) {
	const B = 4096
	build := func(types []byte) []byte {
		var buf bytes.Buffer
		for _, typ := range types {
			data := []byte{0xAA}
			h := make([]byte, 7)
			put := binaryPut(h, naiveCRC(typ, data), uint16(len(data)), typ)
			buf.Write(put)
			buf.Write(data)
		}
		return buf.Bytes()
	}
	cases := []struct {
		name  string
		types []byte
		want  error
	}{
		{"middle without first", []byte{TypeMiddle, TypeLast}, ErrTypeSequence},
		{"last without first", []byte{TypeLast}, ErrTypeSequence},
		{"first while open", []byte{TypeFirst, TypeFirst, TypeLast}, ErrTypeSequence},
		{"full while open", []byte{TypeFirst, TypeFull}, ErrTypeSequence},
		{"type zero valid crc", nil, nil},
	}
	for _, tc := range cases {
		if tc.name == "type zero valid crc" {
			// Build a fragment with invalid type byte 0 and correct CRC,
			// isolating the type-sequence verdict after checksum.
			var buf bytes.Buffer
			data := []byte{0x01}
			h := binaryPut(nil, naiveCRC(0, data), 1, 0)
			buf.Write(h)
			buf.Write(data)
			r, _ := NewReader(bytes.NewReader(buf.Bytes()), B)
			_, _, err := r.Next()
			if !errors.Is(err, ErrTypeSequence) {
				t.Fatalf("invalid type 0: err=%v want ErrTypeSequence", err)
			}
			continue
		}
		data := build(tc.types)
		r, _ := NewReader(bytes.NewReader(data), B)
		var saw error
		for {
			_, _, err := r.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				saw = err
				if errors.Is(err, ErrTruncated) {
					break
				}
			}
		}
		if !errors.Is(saw, tc.want) {
			t.Fatalf("%s: saw=%v want=%v", tc.name, saw, tc.want)
		}
		t.Logf("输入: 类型序列 %d；输出: %v；判定依据: 类型非法/序列非法", tc.types, saw)
	}
}

// TestLengthOutOfBlock crafts a header whose declared length exceeds
// the block, with otherwise arbitrary bytes; verdict must be length
// before any data read.
func TestLengthOutOfBlock(t *testing.T) {
	const B = 16
	data := make([]byte, B)
	// header at offset 0 declares length 60000, impossible in block 16.
	put := binaryPut(data[:7], 0xDEADBEEF, 60000, TypeFull)
	copy(data[:7], put)
	r, _ := NewReader(bytes.NewReader(data), B)
	_, off, err := r.Next()
	if !errors.Is(err, ErrLengthOutOfBlock) {
		t.Fatalf("err=%v want ErrLengthOutOfBlock", err)
	}
	var ce *CorruptError
	errors.As(err, &ce)
	if ce.Offset != 0 || off != 0 {
		t.Fatalf("offset=%d/%d want 0", ce.Offset, off)
	}
	// Recovery lands at block 1, which is all-zero padding => EOF.
	_, _, next := r.Next()
	if next != io.EOF {
		t.Fatalf("after recovery want EOF, got %v", next)
	}
	t.Logf("输入: 头部声明长度 60000（块=%d）；输出: ErrLengthOutOfBlock@0，恢复到块边界 %d；判定依据: 仅凭头部先判长度",
		B, B)
}

// binaryPut encodes a 7-byte fragment header into dst (must have cap 7).
func binaryPut(dst []byte, crc uint32, length uint16, typ byte) []byte {
	out := make([]byte, 7)
	out[0] = byte(crc)
	out[1] = byte(crc >> 8)
	out[2] = byte(crc >> 16)
	out[3] = byte(crc >> 24)
	out[4] = byte(length)
	out[5] = byte(length >> 8)
	out[6] = typ
	return out
}
