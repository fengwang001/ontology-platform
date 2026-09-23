package enc

import (
	"errors"

	"ontology/match"
	"ontology/window"
)

const DefaultBlockSize = 1 << 16

var (
	ErrIllegalConfig = errors.New("enc: illegal configuration")
	ErrClosed        = errors.New("enc: encoder is closed")
)

type Config struct {
	WindowCap  int
	ChainLimit int
	BlockSize  int
}

type Encoder struct {
	cfg    Config
	win    *window.Window
	m      *match.Matcher
	out    []byte
	closed bool
}

func NewEncoder(cfg Config) (*Encoder, error) {
	if cfg.WindowCap <= 0 || cfg.ChainLimit <= 0 {
		return nil, ErrIllegalConfig
	}
	return &Encoder{cfg: cfg}, nil
}

func (e *Encoder) Write(p []byte) (int, error) { return len(p), nil }

func (e *Encoder) Flush() error { return nil }

func (e *Encoder) Close() error {
	e.closed = true
	return nil
}

func (e *Encoder) Bytes() []byte { return e.out }

func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	return nil, nil
}
