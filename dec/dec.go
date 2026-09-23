package dec

import (
	"errors"
	"io"
)

var (
	ErrInvalidWindow = errors.New("dec: window must be positive")
	ErrInvalidLimit  = errors.New("dec: output limit must be non-negative")
	ErrClosed        = errors.New("dec: decompressor is closed")
)

type Config struct {
	WindowSize  int
	OutputLimit int64
}

type Reader struct{}

func NewReader(w io.Writer, cfg Config) (*Reader, error) { return nil, nil }

func (r *Reader) Write(p []byte) (int, error) { return 0, nil }

func (r *Reader) Close() error { return nil }

func (r *Reader) Output() []byte { return nil }
