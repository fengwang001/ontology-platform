package dec

import (
	"errors"

	"ontology/window"
)

var (
	ErrInvalidConfig = errors.New("dec: invalid configuration")
	ErrClosed        = errors.New("dec: decoder is closed")
)

type Config struct {
	WindowCapacity int
	MaxOutput      uint64
}

type Decoder struct{}

func New(cfg Config) (*Decoder, error) { return &Decoder{}, nil }

func (d *Decoder) Write(p []byte) (int, error) { return 0, nil }

func (d *Decoder) Close() error { return nil }

func (d *Decoder) Output() []byte { return nil }

var _ = window.Window{}
