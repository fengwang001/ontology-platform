package framing

import (
	"errors"
	"testing"
)

// Regression: Close never inspected r.buf and never set closeErr, so a
// buffered partial frame was reported as a clean end of stream.
func TestCloseIncompleteRegression(t *testing.T) {
	stream := encode([]byte("payload-data"))

	// Partial length header.
	for _, n := range []int{1, 2, 3} {
		r := New(1 << 20)
		if _, err := r.Feed(stream[:n]); err != nil {
			t.Fatalf("Feed: %v", err)
		}
		if err := r.Close(); !errors.Is(err, ErrIncomplete) {
			t.Fatalf("Close with %d header bytes: want ErrIncomplete, got %v", n, err)
		}
		if err := r.Close(); !errors.Is(err, ErrIncomplete) {
			t.Fatalf("repeat Close with %d header bytes: want ErrIncomplete, got %v", n, err)
		}
	}

	// Complete length header but short payload.
	r := New(1 << 20)
	if _, err := r.Feed(stream[:headerLen+2]); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if err := r.Close(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Close with short payload: want ErrIncomplete, got %v", err)
	}
}
