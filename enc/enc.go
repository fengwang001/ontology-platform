package enc

import (
	"errors"

	"ontology/match"
	"ontology/window"
)

const (
	DefaultWindowCapacity = 1 << 15
	DefaultChainLimit     = 64
	DefaultBlockSize      = 1 << 14
)

var (
	ErrInvalidConfig   = errors.New("enc: invalid configuration")
	ErrInvalidArgument = errors.New("enc: invalid argument")
	ErrClosed          = errors.New("enc: encoder is closed")
)

type Config struct {
	WindowCapacity int
	ChainLimit     int
}

type Encoder struct{}

func New(cfg Config) (*Encoder, error) { return &Encoder{}, nil }

func (e *Encoder) Write(p []byte) (int, error) { return 0, nil }

func (e *Encoder) Flush() error { return nil }

func (e *Encoder) Close() error { return nil }

func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	return nil, nil
}

var _ = match.MinLength
var _ = window.Window{}
