package dec

import (
	"errors"

	"ontology/window"
)

var (
	ErrIllegalConfig = errors.New("enc: illegal configuration")
	ErrTruncated     = errors.New("dec: compressed stream is truncated")
	ErrClosed        = errors.New("dec: decoder is closed")
)

type Config struct {
	WindowCap  int
	MaxOutput  int64
}

type Decoder struct {
	cfg Config
	win *window.Window
	out []byte
}

func NewDecoder(cfg Config) (*Decoder, error) {
	if cfg.WindowCap <= 0 {
		return nil, ErrIllegalConfig
	}
	return &Decoder{}, nil
}

func (d *Decoder) Write(p []byte) (int, error) { return len(p), nil }

func (d *Decoder) Close() error { return nil }

func (d *Decoder) Output() []byte { return d.out }
