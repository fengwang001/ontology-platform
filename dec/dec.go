package dec

import (
	"errors"

"ontology/window"
)

var (
	ErrInvalidConfig = errors.New("lz77: invalid decompressor config")
	ErrClosed        = errors.New("lz77: decompressor is closed")
)

type Config struct {
	WindowCapacity int
	OutputLimit    uint64
}

type Reader struct {
	history *window.Window
	config  Config
	buf     []byte
	offset  int
	pending []byte
}

func NewReader(config Config) (*Reader, error) { return nil, nil }

func (r *Reader) Write(data []byte) (int, error) { return 0, nil }

func (r *Reader) Output() []byte { return nil }

func (r *Reader) Close() error { return nil }
