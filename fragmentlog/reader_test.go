package fragmentlog

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"testing"
)

// realReadAll drains a Reader into the event representation used by the
// naive reference reader.
func realReadAll(t *testing.T, rd *Reader) []event {
	t.Helper()
	var events []event
	for {
		rec, off, err := rd.Next()
		switch {
		case errors.Is(err, io.EOF):
			return append(events, event{kind: "eof"})
		case err != nil:
			var ce *CorruptError
			if !errors.As(err, &ce) {
				t.Fatalf("non-CorruptError from Next: %v", err)
			}
			events = append(events, event{kind: "error", cls: ce.Kind, off: ce.Offset, why: ce.Detail})
		default:
			events = append(events, event{kind: "record", rec: rec, off: off})
		}
	}
}

func mustReader(t *testing.T, data []byte, blockSize int) *Reader {
	t.Helper()
	rd, err := NewReader(bytes.NewReader(data), blockSize)
	if err != nil {
		t.Fatal(err)
	}
	return rd
}

// rewriteFragment patches the fragment header at off in data, fixing the
// crc so only the intended field changes the parse.
func rewriteFragment(data []byte, off int, typ byte, length int) {
	frag := data[off+HeaderSize : off+HeaderSize+length]
	binary.LittleEndian.PutUint32(data[off:off+4],
		crc32.ChecksumIEEE(append([]byte{typ}, frag...)))
	binary.LittleEndian.PutUint16(data[off+4:off+6], uint16(length))
	data[off+6] = typ
}

func TestLengthErrorJudgedFromHeader(t *testing.T) {
	const B = 16
	// Block 0: header claims 10 data bytes, but only 9 fit after a header.
	var stream []byte
	stream = append(stream, make([]byte, B)...)
	binary.LittleEndian.PutUint16(stream[4:6], 10)
	stream[6] = TypeFull
	// Block 1: a valid record.
	stream, _ = naiveAppend(B, stream, []byte("hi"))

	rd := mustReader(t, stream, B)
	_, off, err := rd.Next()
	var ce *CorruptError
	if !errors.As(err, &ce) || !errors.Is(err, ErrLength) || off != 0 {
		t.Fatalf("want ErrLength at 0, got (%v, %d)", err, off)
	}
	t.Logf("输入: 头声明长度 10 > 块内剩余 9 -> 输出: %v (判定: 仅凭头部即判, 未读数据)", ce)
	rec, off, err := rd.Next()
	if err != nil || string(rec) != "hi" || off != B {
		t.Fatalf("recovery: got (%q, %d, %v), want (hi, %d, nil)", rec, off, err, B)
	}
	t.Logf("恢复: 跳到下一块边界 %d, 读回记录 %q", B, rec)
	if _, _, err := rd.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF, got %v", err)
	}
}

func TestChecksumErrorAndRecovery(t *testing.T) {
	const B = 16
	var stream []byte
	var o1, o2, o3 int64
	stream, o1 = naiveAppend(B, stream, []byte("hello"))
	stream, o2 = naiveAppend(B, stream, []byte("world"))
	stream, o3 = naiveAppend(B, stream, []byte("!"))
	// Corrupt one data byte of "world".
	stream[o2+HeaderSize] ^= 0xFF

	rd := mustReader(t, stream, B)
	rec, off, err := rd.Next()
	if err != nil || string(rec) != "hello" || off != o1 {
		t.Fatalf("first record: (%q, %d, %v)", rec, off, err)
	}
	_, off, err = rd.Next()
	if !errors.Is(err, ErrChecksum) || off != o2 {
		t.Fatalf("want ErrChecksum at %d, got (%v, %d)", o2, err, off)
	}
	t.Logf("输入: 翻转记录2数据一字节 -> 输出: %v (判定: crc 不符), 错误偏移=%d", err, off)
	rec, off, err = rd.Next()
	if err != nil || string(rec) != "!" || off != o3 {
		t.Fatalf("recovery: (%q, %d, %v), want (!, %d, nil)", rec, off, err, o3)
	}
	t.Logf("恢复: 从出错片段所在块的下一边界继续, 读回 %q @%d", rec, off)
}

func TestSequenceErrors(t *testing.T) {
	const B = 16
	mk := func(mutate func(stream []byte)) []byte {
		var stream []byte
		stream, _ = naiveAppend(B, stream, []byte("hello"))
		stream, _ = naiveAppend(B, stream, []byte("world"))
		mutate(stream)
		return stream
	}

	// (a) Type value outside 1..4.
	stream := mk(func(s []byte) { rewriteFragment(s, 0, 9, 5) })
	rd := mustReader(t, stream, B)
	if _, off, err := rd.Next(); !errors.Is(err, ErrSequence) || off != 0 {
		t.Fatalf("(a) want ErrSequence at 0, got (%v, %d)", err, off)
	}
	t.Logf("(a) 类型值 9 不在 1..4 -> ErrSequence @0 (判定: 类型序列, 在校验通过之后)")

	// (b) Last without a preceding First.
	stream = mk(func(s []byte) { rewriteFragment(s, 0, TypeLast, 5) })
	rd = mustReader(t, stream, B)
	if _, off, err := rd.Next(); !errors.Is(err, ErrSequence) || off != 0 {
		t.Fatalf("(b) want ErrSequence at 0, got (%v, %d)", err, off)
	}
	t.Logf("(b) 无首片的末片 -> ErrSequence @0")

	// (c) First, then a Full while the record is unfinished.
	stream = mk(func(s []byte) { rewriteFragment(s, 0, TypeFirst, 5) })
	rd = mustReader(t, stream, B)
	_, off, err := rd.Next()
	if !errors.Is(err, ErrSequence) || off != B {
		t.Fatalf("(c) want ErrSequence at %d, got (%v, %d)", B, err, off)
	}
	t.Logf("(c) 首片之后又遇完整片段 -> ErrSequence @%d (判定: 记录未收尾)", B)

	// (d) Middle without a preceding First.
	stream = mk(func(s []byte) { rewriteFragment(s, 0, TypeMiddle, 5) })
	rd = mustReader(t, stream, B)
	if _, off, err := rd.Next(); !errors.Is(err, ErrSequence) || off != 0 {
		t.Fatalf("(d) want ErrSequence at 0, got (%v, %d)", err, off)
	}
	t.Logf("(d) 无首片的中片 -> ErrSequence @0")
}
