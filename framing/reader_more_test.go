package framing

import (
	"errors"
	"testing"
)

// Semantics 5: zero-length frames are real frames.
func TestZeroLengthFrame(t *testing.T) {
	r := New(1 << 20)
	frames, err := r.Feed(encode([]byte{}, []byte("mid"), []byte{}))
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(frames) != 3 {
		t.Fatalf("want 3 frames, got %d", len(frames))
	}
	for _, i := range []int{0, 2} {
		if frames[i] == nil {
			t.Fatalf("frame %d: empty frame must be non-nil", i)
		}
		if len(frames[i]) != 0 {
			t.Fatalf("frame %d: want len 0, got %d", i, len(frames[i]))
		}
	}
	if string(frames[1]) != "mid" {
		t.Fatalf("frame 1: want %q, got %q", "mid", frames[1])
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close at frame boundary: %v", err)
	}
}

// Semantics 6: returned frames are isolated copies.
func TestCopyIsolation(t *testing.T) {
	r := New(1 << 20)
	p := encode([]byte("aaaa"), []byte("bbbb"))
	frames, err := r.Feed(p)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}

	// Mutating the caller's input must not affect returned frames.
	for i := range p {
		p[i] = 'X'
	}
	if string(frames[0]) != "aaaa" || string(frames[1]) != "bbbb" {
		t.Fatalf("input mutation leaked into frames: %q %q", frames[0], frames[1])
	}

	// Mutating a returned frame must not affect the reader or later frames.
	frames[0][0] = 'Z'
	frames2, err := r.Feed(encode([]byte("cccc")))
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if string(frames[1]) != "bbbb" || string(frames2[0]) != "cccc" {
		t.Fatalf("frame mutation leaked: %q %q", frames[1], frames2[0])
	}
}

// Semantics 7: Close semantics.
func TestClose(t *testing.T) {
	// Clean boundary: nil.
	r := New(1 << 20)
	if _, err := r.Feed(encode([]byte("done"))); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close at boundary: %v", err)
	}

	// 1..3 leftover header bytes: ErrIncomplete.
	stream := encode([]byte("abcdef"))
	for _, n := range []int{1, 2, 3} {
		r := New(1 << 20)
		if _, err := r.Feed(stream[:n]); err != nil {
			t.Fatalf("Feed: %v", err)
		}
		if err := r.Close(); !errors.Is(err, ErrIncomplete) {
			t.Fatalf("Close with %d header bytes: want ErrIncomplete, got %v", n, err)
		}
	}

	// Full header, short payload: ErrIncomplete.
	r = New(1 << 20)
	if _, err := r.Feed(stream[:len(stream)-2]); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if err := r.Close(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Close with short payload: want ErrIncomplete, got %v", err)
	}

	// Idempotent and Feed-after-Close fails.
	if err := r.Close(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("second Close: want ErrIncomplete, got %v", err)
	}
	if _, err := r.Feed([]byte{0}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Feed after Close: want ErrClosed, got %v", err)
	}
}

// Semantics 8: buffer does not grow without bound.
func TestNoUnboundedGrowth(t *testing.T) {
	r := New(1 << 20)
	chunk := encode([]byte("tiny"))
	total := 0
	for i := 0; i < 10000; i++ {
		frames, err := r.Feed(chunk)
		if err != nil {
			t.Fatalf("Feed %d: %v", i, err)
		}
		total += len(frames)
	}
	if total != 10000 {
		t.Fatalf("want 10000 frames, got %d", total)
	}
	if r.Buffered() != 0 {
		t.Fatalf("Buffered: want 0, got %d", r.Buffered())
	}
	if cap(r.buf) > 64 {
		t.Fatalf("internal buffer capacity grew to %d", cap(r.buf))
	}
}
