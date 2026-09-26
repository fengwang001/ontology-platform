// Package dec is the streaming validator and decompressor.
package dec

import "errors"

var ErrConfig = errors.New("dec: invalid configuration")

type Config struct {
	Window     int
	MaxOutput  int64
}

type Decompressor struct{}

func New(cfg Config) (*Decompressor, error) { return nil, ErrConfig }

func (d *Decompressor) Write(p []byte) (int, error) { return 0, nil }

func (d *Decompressor) Close() error { return nil }

func (d *Decompressor) Output() []byte { return nil }

func (d *Decompressor) Consumed() int { return 0 }
