package framing

import (
	"errors"
	"testing"
	"unsafe"
)

// 根因：长度判断写成了 n >= max，把恰好等于 maxFrame 的合法帧误判为超长。
func TestExactMaxFrameIsLegal(t *testing.T) {
	r := New(4)

	frames, err := r.Feed(encode([]byte("1234"))) // n == maxFrame
	if err != nil {
		t.Fatalf("exact-max frame: want nil error, got %v", err)
	}
	assertFramesEqual(t, [][]byte{[]byte("1234")}, frames)
	if err := r.Close(); err != nil {
		t.Fatalf("Close after exact-max frame: %v", err)
	}

	// maxFrame+1 must still fail, and the failure must stick.
	r = New(4)
	if _, err := r.Feed(encode([]byte("12345"))); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversized frame: want ErrFrameTooLarge, got %v", err)
	}
	if _, err := r.Feed(encode([]byte("1"))); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("post-failure Feed: want ErrFrameTooLarge, got %v", err)
	}
}

// 根因：旧实现把 r.buf 的子切片直接当作帧返回，返回帧与内部缓冲区共享底层数组。
func TestReturnedFramesAreCopies(t *testing.T) {
	r := New(1 << 20)
	stream := append(encode([]byte("aaaa")), 0, 0) // one full frame + half a header

	frames, err := r.Feed(stream)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("want 1 frame, got %d", len(frames))
	}
	if r.Buffered() != 2 {
		t.Fatalf("Buffered: want 2, got %d", r.Buffered())
	}

	// If the frame aliases the internal buffer, its capacity range covers
	// the residual bytes r.buf is still holding.
	fp := uintptr(unsafe.Pointer(&frames[0][0]))
	bp := uintptr(unsafe.Pointer(&r.buf[0]))
	if fp <= bp && bp < fp+uintptr(cap(frames[0])) {
		t.Fatal("returned frame aliases the reader's internal buffer")
	}
}

// 根因：Close 把状态置为 closed 后直接返回了从未赋值的 closeErr（恒为 nil）。
func TestCloseReportsIncomplete(t *testing.T) {
	stream := encode([]byte("abcdef"))

	// Leftover header bytes: ErrIncomplete, consistently on repeat.
	r := New(1 << 20)
	if _, err := r.Feed(stream[:3]); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if err := r.Close(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Close with partial header: want ErrIncomplete, got %v", err)
	}
	if err := r.Close(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("second Close: want ErrIncomplete, got %v", err)
	}

	// Full header but short payload: ErrIncomplete.
	r = New(1 << 20)
	if _, err := r.Feed(stream[:len(stream)-1]); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if err := r.Close(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Close with partial payload: want ErrIncomplete, got %v", err)
	}
}
