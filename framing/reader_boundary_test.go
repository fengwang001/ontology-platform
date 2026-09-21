package framing

import (
	"errors"
	"testing"
)

// Regression: the oversized check used n >= max, rejecting frames whose
// payload is exactly maxFrame. Only n > max is too large.
func TestMaxFrameBoundary(t *testing.T) {
	const max = 8

	// Exactly maxFrame payload bytes: legal, must be returned.
	r := New(max)
	frames, err := r.Feed(encode([]byte("12345678")))
	if err != nil {
		t.Fatalf("exactly-max frame: want nil err, got %v", err)
	}
	assertFramesEqual(t, [][]byte{[]byte("12345678")}, frames)
	if err := r.Close(); err != nil {
		t.Fatalf("Close after exactly-max frame: %v", err)
	}

	// One byte over: ErrFrameTooLarge, terminal.
	r = New(max)
	_, err = r.Feed(encode([]byte("123456789")))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("max+1 frame: want ErrFrameTooLarge, got %v", err)
	}
}
