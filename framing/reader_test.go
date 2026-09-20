package framing

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func encode(frames ...[]byte) []byte {
	var buf bytes.Buffer
	for _, f := range frames {
		var hdr [headerLen]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(len(f)))
		buf.Write(hdr[:])
		buf.Write(f)
	}
	return buf.Bytes()
}

func feedAll(t *testing.T, r *Reader, stream []byte, sizes []int) [][]byte {
	t.Helper()
	var got [][]byte
	off := 0
	i := 0
	for off < len(stream) {
		n := sizes[i%len(sizes)]
		i++
		if n == 0 { // exercise empty Feed between chunks
			frames, err := r.Feed(nil)
			if err != nil {
				t.Fatalf("empty Feed: %v", err)
			}
			if len(frames) != 0 {
				t.Fatalf("empty Feed returned %d frames", len(frames))
			}
			continue
		}
		if off+n > len(stream) {
			n = len(stream) - off
		}
		frames, err := r.Feed(stream[off : off+n])
		if err != nil {
			t.Fatalf("Feed: %v", err)
		}
		got = append(got, frames...)
		off += n
	}
	return got
}

func assertFramesEqual(t *testing.T, want, got [][]byte) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("frame count: want %d, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] == nil {
			t.Fatalf("frame %d is nil", i)
		}
		if !bytes.Equal(want[i], got[i]) {
			t.Fatalf("frame %d: want %q, got %q", i, want[i], got[i])
		}
	}
}

// Semantics 1: chunking invariance.
func TestChunkingInvariance(t *testing.T) {
	payloads := [][]byte{
		[]byte("hello"),
		[]byte(""),
		[]byte("a somewhat longer payload spanning more bytes"),
		{0x00, 0x01, 0x02, 0x03},
		[]byte("x"),
	}
	stream := encode(payloads...)
	patterns := [][]int{
		{len(stream)},          // all at once
		{1},                    // one byte at a time
		{2, 0, 3},              // splits inside headers, with empty feeds
		{7, 1, 0, 13},          // splits inside payloads, with empty feeds
		{0, 0, len(stream)},    // leading empty feeds
		{3, 5, 2, 11, 1, 0, 4}, // irregular
	}
	for _, sizes := range patterns {
		got := feedAll(t, New(1<<20), stream, sizes)
		assertFramesEqual(t, payloads, got)
	}
}

// Semantics 2: coalesced frames in a single Feed.
func TestCoalescedFrames(t *testing.T) {
	payloads := [][]byte{[]byte("one"), []byte("two"), []byte("three")}
	frames, err := New(1 << 20).Feed(encode(payloads...))
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	assertFramesEqual(t, payloads, frames)
}

// Semantics 3: partial frames are buffered, not errors.
func TestPartialFrame(t *testing.T) {
	r := New(1 << 20)
	stream := encode([]byte("partial"))

	frames, err := r.Feed(stream[:2]) // half of the length header
	if err != nil || len(frames) != 0 {
		t.Fatalf("half header: frames=%v err=%v", frames, err)
	}
	if r.Buffered() != 2 {
		t.Fatalf("Buffered: want 2, got %d", r.Buffered())
	}

	frames, err = r.Feed(stream[2:6]) // rest of header + 2 payload bytes
	if err != nil || len(frames) != 0 {
		t.Fatalf("partial payload: frames=%v err=%v", frames, err)
	}
	if r.Buffered() != 6 {
		t.Fatalf("Buffered: want 6, got %d", r.Buffered())
	}

	frames, err = r.Feed(stream[6:])
	if err != nil {
		t.Fatalf("final Feed: %v", err)
	}
	assertFramesEqual(t, [][]byte{[]byte("partial")}, frames)
	if r.Buffered() != 0 {
		t.Fatalf("Buffered: want 0, got %d", r.Buffered())
	}
}

// Semantics 4: oversized frame is a terminal error.
func TestFrameTooLarge(t *testing.T) {
	r := New(4)
	stream := encode([]byte("ok"), []byte("this payload is way too long"))

	frames, err := r.Feed(stream)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err: want ErrFrameTooLarge, got %v", err)
	}
	assertFramesEqual(t, [][]byte{[]byte("ok")}, frames) // prior frames kept

	buffered := r.Buffered()
	for i := 0; i < 3; i++ {
		frames, err = r.Feed([]byte{1, 2, 3})
		if !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("post-failure Feed %d: want ErrFrameTooLarge, got %v", i, err)
		}
		if frames != nil {
			t.Fatalf("post-failure Feed %d: want nil frames, got %v", i, frames)
		}
		if r.Buffered() != buffered {
			t.Fatalf("post-failure Buffered changed: %d -> %d", buffered, r.Buffered())
		}
	}
}
