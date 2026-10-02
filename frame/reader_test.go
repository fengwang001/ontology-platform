package frame

import (
	"bytes"
	"errors"
	"testing"
)

// sampleStream 构造含压缩块、原样块、可跳过块与重复标识块的流，
// 返回流字节与期望解出的数据。
func sampleStream() (stream, want []byte) {
	w, _ := NewWriter(1, 1)
	w.Write(bytes.Repeat([]byte{'a'}, 100))
	w.Flush()
	w.Write(incompressible(100))
	w.Flush()
	w.Write(incompressible(10))
	w.Flush()
	w.Close()
	stream = w.Bytes()
	// 在标识块后插入一个可跳过块与重复标识块。
	skip := appendHeader(nil, 0x90, 7)
	skip = append(skip, []byte("ignored")...)
	stream = append(stream[:10],
		append(skip, append(identifierChunk, stream[10:]...)...)...)
	want = append(bytes.Repeat([]byte{'a'}, 100),
		append(incompressible(100), incompressible(10)...)...)
	return stream, want
}

func TestReaderBasic(t *testing.T) {
	stream, want := sampleStream()
	r := NewReader()
	got, err := r.Feed(stream)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("decoded %d bytes, want %d", len(got), len(want))
	}
}

func TestReaderAllSplitPoints(t *testing.T) {
	stream, want := sampleStream()
	for cut := 0; cut <= len(stream); cut++ {
		r := NewReader()
		var got []byte
		d, err := r.Feed(stream[:cut])
		if err != nil {
			t.Fatalf("cut=%d first Feed: %v", cut, err)
		}
		got = append(got, d...)
		d, err = r.Feed(stream[cut:])
		if err != nil {
			t.Fatalf("cut=%d second Feed: %v", cut, err)
		}
		got = append(got, d...)
		if err := r.Close(); err != nil {
			t.Fatalf("cut=%d Close: %v", cut, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("cut=%d decoded mismatch", cut)
		}
	}
	// 逐字节喂入。
	r := NewReader()
	var got []byte
	for _, b := range stream {
		d, err := r.Feed([]byte{b})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, d...)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("byte-by-byte decoded mismatch")
	}
}

func TestReaderBitFlips(t *testing.T) {
	// 无skippable 块的流：压缩块 + 原样块。
	w, _ := NewWriter(1, 1)
	w.Write(bytes.Repeat([]byte{'a'}, 100))
	w.Flush()
	w.Write(incompressible(100))
	w.Flush()
	w.Close()
	stream := w.Bytes()
	_, want := sampleStream()
	want = append(bytes.Repeat([]byte{'a'}, 100), incompressible(100)...)

	for pos := 0; pos < len(stream); pos++ {
		mut := bytes.Clone(stream)
		mut[pos] ^= 0xFF
		r := NewReader()
		got, err := r.Feed(mut)
		if err == nil {
			err = r.Close()
		}
		if err == nil && bytes.Equal(got, want) {
			t.Errorf("flip at %d: undetected (no error, same output)", pos)
		}
	}
}

func TestReaderTruncations(t *testing.T) {
	stream, want := sampleStream()
	_ = want
	// 计算块边界。
	bounds := map[int]bool{0: true}
	for i := 0; i < len(stream); {
		n := int(stream[i+1]) | int(stream[i+2])<<8 | int(stream[i+3])<<16
		i += 4 + n
		bounds[i] = true
	}
	for cut := 0; cut < len(stream); cut++ {
		r := NewReader()
		r.Feed(stream[:cut])
		err := r.Close()
		if cut == 0 {
			if !errors.Is(err, ErrNoIdentifier) {
				t.Errorf("cut=0: got %v, want ErrNoIdentifier", err)
			}
			continue
		}
		if bounds[cut] {
			if err != nil {
				t.Errorf("cut=%d at boundary: got %v, want nil", cut, err)
			}
			continue
		}
		if !errors.Is(err, ErrTruncated) {
			t.Errorf("cut=%d: got %v, want ErrTruncated", cut, err)
		}
		var fe *FrameError
		if !errors.As(err, &fe) {
			t.Errorf("cut=%d: not a *FrameError", cut)
		}
	}
}

func TestReaderErrorOffsets(t *testing.T) {
	// 构造：标识块(10) + 原样块(4+4+10=18) → 下一块偏移 28。
	w, _ := NewWriter(1, 1)
	w.Write(incompressible(10))
	w.Close()
	base := w.Bytes()
	const nextOff = 28

	mkErr := func(tail []byte) (int64, error) {
		r := NewReader()
		_, err := r.Feed(append(bytes.Clone(base), tail...))
		if err == nil {
			err = r.Close()
		}
		var fe *FrameError
		if !errors.As(err, &fe) {
			return -1, err
		}
		return fe.Offset, fe.Err
	}

	cases := []struct {
		name string
		tail []byte
		off  int64
		kind error
	}{
		{"reserved", []byte{0x02, 0, 0, 0}, nextOff, ErrReserved},
		{"len too small", []byte{0x01, 3, 0, 0}, nextOff, ErrChunkLen},
		{"len too big", []byte{0x00, 0x45, 0, 0x01}, nextOff, ErrChunkLen},
		{"bad identifier len", []byte{0xFF, 5, 0, 0}, nextOff, ErrBadIdentifier},
		{"bad identifier payload", append([]byte{0xFF, 6, 0, 0}, []byte("xxxxxx")...), nextOff, ErrBadIdentifier},
		{"truncated header", []byte{0x01, 9}, nextOff, ErrTruncated},
		{"truncated payload", []byte{0x01, 20, 0, 0, 1, 2, 3}, nextOff, ErrTruncated},
		{"truncated skippable", append([]byte{0x80, 100, 0, 0}, bytes.Repeat([]byte{0}, 30)...), nextOff, ErrTruncated},
		{"decode truncated token", []byte{0x00, 5, 0, 0, 0, 0, 0, 0, 0x80}, nextOff, ErrDecode},
		{"checksum", []byte{0x01, 5, 0, 0, 0, 0, 0, 0, 'z'}, nextOff, ErrChecksum},
	}
	for _, c := range cases {
		off, kind := mkErr(c.tail)
		if off != c.off || kind != c.kind {
			t.Errorf("%s: got (off=%d, %v), want (off=%d, %v)",
				c.name, off, kind, c.off, c.kind)
		}
	}

	// 首块类型错误：ErrNoIdentifier 先于其他判定，偏移 0。
	r := NewReader()
	if _, err := r.Feed([]byte{0x7F, 0, 0, 0}); !errors.Is(err, ErrNoIdentifier) {
		t.Errorf("first chunk type: got %v", err)
	}
	// 空流：Close 报 ErrNoIdentifier，偏移 0。
	r = NewReader()
	err := r.Close()
	var fe *FrameError
	if !errors.As(err, &fe) || fe.Offset != 0 || !errors.Is(err, ErrNoIdentifier) {
		t.Errorf("empty stream Close: got %v", err)
	}
	// 解出超过 65536：ErrDecode 先于校验和。
	payload := bytes.Repeat([]byte{0xFF, 'a'}, 505) // 65650 > 65536
	tail := appendHeader(nil, 0x00, 4+len(payload))
	tail = append(tail, 0, 0, 0, 0)
	tail = append(tail, payload...)
	off, kind := mkErr(tail)
	if off != nextOff || kind != ErrDecode {
		t.Errorf("decode overflow: got (off=%d, %v)", off, kind)
	}
}

func TestReaderStickyError(t *testing.T) {
	r := NewReader()
	_, err1 := r.Feed([]byte{0x01, 0, 0, 0})
	if !errors.Is(err1, ErrNoIdentifier) {
		t.Fatalf("got %v", err1)
	}
	got, err2 := r.Feed(identifierChunk)
	if err2 != err1 || got != nil {
		t.Errorf("sticky: got (%v, %d bytes), want same error, no output", err2, len(got))
	}
	if err := r.Close(); err != err1 {
		t.Errorf("Close after error: got %v", err)
	}
}

func TestReaderChunkLenEarly(t *testing.T) {
	// 块头收齐即判，不等负载。
	r := NewReader()
	if _, err := r.Feed(identifierChunk); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Feed([]byte{0x01, 3, 0, 0}); !errors.Is(err, ErrChunkLen) {
		t.Fatalf("got %v, want ErrChunkLen", err)
	}
}

func TestReaderMaxBuffered(t *testing.T) {
	// 最大原样块逐字节喂入：峰值恰为 4+65540。
	w, _ := NewWriter(1, 1)
	w.Write(incompressible(maxUncompressed))
	w.Close()
	r := NewReader()
	for _, b := range w.Bytes() {
		if _, err := r.Feed([]byte{b}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if r.maxBuffered != maxBuffered {
		t.Errorf("maxBuffered = %d, want %d", r.maxBuffered, maxBuffered)
	}
	// 可跳过块不缓冲负载。
	r = NewReader()
	big := appendHeader(nil, 0x80, 1<<20)
	big = append(big, make([]byte, 1<<20)...)
	stream := append(identifierChunk, big...)
	if _, err := r.Feed(stream); err != nil {
		t.Fatal(err)
	}
	// 仅标识块本身（10 字节）被缓冲，可跳过块负载不进入缓冲。
	if r.maxBuffered > len(identifierChunk) {
		t.Errorf("skippable maxBuffered = %d, want <= %d", r.maxBuffered, len(identifierChunk))
	}
}

func TestReaderEmptyAndZeroFeeds(t *testing.T) {
	r := NewReader()
	got, err := r.Feed(nil)
	if err != nil || got != nil {
		t.Errorf("Feed(nil) = (%v, %v)", got, err)
	}
	got, err = r.Feed([]byte{})
	if err != nil || got != nil {
		t.Errorf("Feed(empty) = (%v, %v)", got, err)
	}
	if err := r.Close(); !errors.Is(err, ErrNoIdentifier) {
		t.Errorf("Close = %v, want ErrNoIdentifier", err)
	}
}
