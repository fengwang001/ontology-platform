package framing

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func encode(frames ...[]byte) []byte {
	var out []byte
	for _, f := range frames {
		var hdr [headerLen]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(len(f)))
		out = append(out, hdr[:]...)
		out = append(out, f...)
	}
	return out
}

// feedAll 按 sizes 循环给出的块长把 data 喂给 r，汇总切出的帧。
func feedAll(r *Reader, data []byte, sizes []int) ([][]byte, error) {
	var frames [][]byte
	off, i := 0, 0
	for off < len(data) {
		n := sizes[i%len(sizes)]
		i++
		if n == 0 { // 空切片也要喂，验证不干扰状态
			if _, err := r.Feed(nil); err != nil {
				return frames, err
			}
			continue
		}
		if n > len(data)-off {
			n = len(data) - off
		}
		got, err := r.Feed(data[off : off+n])
		frames = append(frames, got...)
		if err != nil {
			return frames, err
		}
		off += n
	}
	return frames, nil
}

func equalFrames(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// 语义 1：分块不变性——同一字节流无论怎么切分，切出的帧序列完全一致。
func TestChunkingInvariance(t *testing.T) {
	want := [][]byte{
		[]byte("hello"),
		{},
		[]byte("a longer payload crossing many chunk boundaries"),
		{0x00, 0x01, 0x02, 0xff},
		[]byte("x"),
	}
	stream := encode(want...)

	patterns := map[string][]int{
		"all at once":    {len(stream)},
		"one byte":       {1},
		"header splits":  {2, 1, 3, 1, 2},
		"payload splits": {5, 3, 7, 2},
		"with empties":   {0, 3, 0, 1, 0, 4, 2},
		"odd cycle":      {1, 0, 6, 2, 0, 11, 3},
	}
	for name, sizes := range patterns {
		t.Run(name, func(t *testing.T) {
			r := New(1024)
			got, err := feedAll(r, stream, sizes)
			if err != nil {
				t.Fatalf("Feed: %v", err)
			}
			if !equalFrames(got, want) {
				t.Fatalf("frames mismatch:\n got %q\nwant %q", got, want)
			}
			if r.Buffered() != 0 {
				t.Fatalf("Buffered = %d, want 0", r.Buffered())
			}
			if err := r.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
		})
	}
}

// 语义 2：粘包——一次 Feed 含多条完整帧时一次全部按序返回。
func TestStickyFrames(t *testing.T) {
	want := [][]byte{[]byte("a"), []byte("bb"), {}, []byte("ccc")}
	r := New(16)
	got, err := r.Feed(encode(want...))
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if !equalFrames(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if r.Buffered() != 0 {
		t.Fatalf("Buffered = %d, want 0", r.Buffered())
	}
}

// 语义 3：半包——不足以成帧时不报错，残留计入 Buffered，到齐后成帧。
func TestPartialFrame(t *testing.T) {
	r := New(16)
	stream := encode([]byte("payload"))

	got, err := r.Feed(stream[:2]) // 长度字段未齐
	if err != nil || len(got) != 0 {
		t.Fatalf("after 2 header bytes: frames=%v err=%v", got, err)
	}
	if r.Buffered() != 2 {
		t.Fatalf("Buffered = %d, want 2", r.Buffered())
	}

	got, err = r.Feed(stream[2:6]) // 长度字段齐了，负载未齐
	if err != nil || len(got) != 0 {
		t.Fatalf("after header: frames=%v err=%v", got, err)
	}
	if r.Buffered() != 6 {
		t.Fatalf("Buffered = %d, want 6", r.Buffered())
	}

	got, err = r.Feed(stream[6:])
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(got) != 1 || string(got[0]) != "payload" {
		t.Fatalf("got %q, want [payload]", got)
	}
	if r.Buffered() != 0 {
		t.Fatalf("Buffered = %d, want 0", r.Buffered())
	}
}

// 语义 4：超长帧——返回 ErrFrameTooLarge 并进入终止态，
// 后续 Feed 一律返回同一错误且 Buffered 不再变化。
func TestFrameTooLarge(t *testing.T) {
	r := New(8)
	var hdr [headerLen]byte
	binary.BigEndian.PutUint32(hdr[:], 9)

	got, err := r.Feed(hdr[:])
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err = %v, want ErrFrameTooLarge", err)
	}
	if len(got) != 0 {
		t.Fatalf("frames = %v, want none", got)
	}
	buffered := r.Buffered()
	if buffered != headerLen {
		t.Fatalf("Buffered = %d, want %d (字节不得被吞掉)", buffered, headerLen)
	}

	for i := 0; i < 3; i++ {
		if _, err := r.Feed([]byte("more data")); !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("feed %d after error: err = %v, want ErrFrameTooLarge", i, err)
		}
		if r.Buffered() != buffered {
			t.Fatalf("feed %d after error: Buffered = %d, want %d", i, r.Buffered(), buffered)
		}
	}
}

// 超长帧之前已切出的完整帧必须随错误一起返回。
func TestFramesBeforeTooLarge(t *testing.T) {
	r := New(4)
	stream := encode([]byte("ok"))
	var hdr [headerLen]byte
	binary.BigEndian.PutUint32(hdr[:], 100)
	stream = append(stream, hdr[:]...)

	got, err := r.Feed(stream)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err = %v, want ErrFrameTooLarge", err)
	}
	if len(got) != 1 || string(got[0]) != "ok" {
		t.Fatalf("frames = %q, want [ok]", got)
	}
}
