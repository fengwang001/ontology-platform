// Package enc is the streaming LZ77 compressor and the parallel block compressor.
package enc

import (
	"errors"

	"ontology/match"
)

var ErrConfig = errors.New("enc: invalid configuration")

type Config struct {
	Window     int
	ChainLimit int
}

type Compressor struct{}

func New(cfg Config) (*Compressor, error) { return nil, ErrConfig }

func (c *Compressor) Write(p []byte) (int, error) { return 0, nil }

func (c *Compressor) Flush() error { return nil }

func (c *Compressor) Close() error { return nil }

func (c *Compressor) Bytes() []byte { return nil }

// CompressParallel compresses independent blocks sharing previous-block dictionaries.
func CompressParallel(data []byte, blockSize int, workers int) ([]byte, error) {
	return nil, ErrConfig
}

var _ = match.MinMatch
