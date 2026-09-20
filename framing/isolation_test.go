package framing

import (
	"errors"
	"testing"
)

// 语义 5：零长度帧合法——N=0 必须作为一条非 nil 的空帧返回。
func TestZeroLengthFrame(t *testing.T) {
	r := New(16)
	got, err := r.Feed(encode([]byte{}, []byte("a"), []byte{}))
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d frames, want 3", len(got))
	}
	for _, i := range []int{0, 2} {
		if got[i] == nil {
			t.Fatalf("frame %d is nil, want non-nil empty slice", i)
		}
		if len(got[i]) != 0 {
			t.Fatalf("frame %d len = %d, want 0", i, len(got[i]))
		}
	}
	if string(got[1]) != "a" {
		t.Fatalf("frame 1 = %q, want %q", got[1], "a")
	}
}

// 语义 6：拷贝隔离——改写返回的帧或改写喂入的 p 都不影响读取器与其他帧。
func TestCopyIsolation(t *testing.T) {
	r := New(16)
	p := encode([]byte("hello"), []byte("world"))

	got, err := r.Feed(p)
	if err != nil || len(got) != 2 {
		t.Fatalf("Feed: frames=%v err=%v", got, err)
	}
	for i := range p { // Feed 之后改写 p，不得影响已返回的帧
		p[i] = 0
	}
	if string(got[0]) != "hello" || string(got[1]) != "world" {
		t.Fatalf("frames mutated by caller buffer: %q", got)
	}

	got[0][0] = 'X' // 改写返回的帧，不得影响读取器状态
	next, err := r.Feed(encode([]byte("again")))
	if err != nil || len(next) != 1 || string(next[0]) != "again" {
		t.Fatalf("after mutating returned frame: frames=%q err=%v", next, err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// 语义 6（半包路径）：喂入的 p 在半包状态下被改写，不得污染残留数据。
func TestCopyIsolationPartial(t *testing.T) {
	r := New(16)
	stream := encode([]byte("safe"))
	if _, err := r.Feed(stream[:3]); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	for i := range stream {
		stream[i] = 0xff
	}
	got, err := r.Feed(encode([]byte("safe"))[3:])
	if err != nil || len(got) != 1 || string(got[0]) != "safe" {
		t.Fatalf("frames=%q err=%v, want [safe]", got, err)
	}
}

// 语义 7：Close 语义——帧边界结束为 nil；残留 1~3 字节长度字段或
// 负载不全均为 ErrIncomplete；Close 后 Feed 报错；Close 可重复且结果一致。
func TestCloseSemantics(t *testing.T) {
	r := New(16)
	if _, err := r.Feed(encode([]byte("done"))); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close at frame boundary: %v, want nil", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close: %v, want nil (结果须一致)", err)
	}
	if _, err := r.Feed([]byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatalf("Feed after Close: %v, want ErrClosed", err)
	}

	for _, n := range []int{1, 2, 3} { // 残留 1~3 字节长度字段
		r := New(16)
		if _, err := r.Feed(encode([]byte("x"))[:n]); err != nil {
			t.Fatalf("Feed: %v", err)
		}
		if err := r.Close(); !errors.Is(err, ErrIncomplete) {
			t.Fatalf("Close with %d leftover header bytes: %v, want ErrIncomplete", n, err)
		}
		if err := r.Close(); !errors.Is(err, ErrIncomplete) {
			t.Fatalf("second Close: %v, want ErrIncomplete (结果须一致)", err)
		}
	}

	r = New(16) // 长度字段齐了但负载不全
	stream := encode([]byte("truncated"))
	if _, err := r.Feed(stream[:len(stream)-2]); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if err := r.Close(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Close with partial payload: %v, want ErrIncomplete", err)
	}
}

// 语义 8：不得无界增长——连续喂入大量小帧后 Buffered 归零且容量有界。
func TestBoundedGrowth(t *testing.T) {
	r := New(16)
	chunk := encode([]byte("ab"))
	for i := 0; i < 10000; i++ {
		got, err := r.Feed(chunk)
		if err != nil {
			t.Fatalf("feed %d: %v", i, err)
		}
		if len(got) != 1 {
			t.Fatalf("feed %d: got %d frames, want 1", i, len(got))
		}
	}
	if r.Buffered() != 0 {
		t.Fatalf("Buffered = %d, want 0", r.Buffered())
	}
	if c := cap(r.buf); c > shrinkThreshold {
		t.Fatalf("internal buffer cap = %d, want <= %d (不得随流长度线性增长)", c, shrinkThreshold)
	}
}
