package framing

import "testing"

// Regression: Feed handed out sub-slices of r.buf instead of copies. A
// zero-length frame was therefore returned with spare capacity pointing
// straight into the reader's buffered bytes; appending to it corrupted the
// next frame's length header.
func TestEmptyFrameAppendDoesNotCorruptReader(t *testing.T) {
	// Zero-length frame followed by the first two bytes of the next frame's
	// length header (0x00 0x00 ...), which remain buffered.
	r := New(1 << 20)
	frames, err := r.Feed([]byte{0, 0, 0, 0, 0x00, 0x00})
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(frames) != 1 || frames[0] == nil || len(frames[0]) != 0 {
		t.Fatalf("want one non-nil empty frame, got %v", frames)
	}

	// Callers may legitimately append to a returned frame. With the
	// aliasing bug this overwrites the buffered next length header.
	got := append(frames[0], 0x00, 0x05)
	if string(got) != "\x00\x05" {
		t.Fatalf("append result: want %q, got %q", "\x00\x05", got)
	}

	// Complete the next frame: payload "hello", length 5.
	frames, err = r.Feed([]byte{0x00, 0x05, 'h', 'e', 'l', 'l', 'o'})
	if err != nil {
		t.Fatalf("completing next frame: %v", err)
	}
	if len(frames) != 1 || string(frames[0]) != "hello" {
		t.Fatalf("want one frame %q, got %v", "hello", frames)
	}
}
