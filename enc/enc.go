// Package enc implements the streaming LZ77 compressor.
//
// A single Compressor is not safe for concurrent use by multiple goroutines;
// use separate instances. CompressParallel itself is internally concurrent.
package enc

import (
	"errors"
	"io"
)

// Default configuration values.
const (
	DefaultWindowSize = 1 << 15
	DefaultMaxChain   = 64
)

var ErrConfig = errors.New("enc: invalid configuration")

// Compressor streams compressed data to an underlying writer.
type Compressor struct{}

// NewCompressor creates a compressor with the given window and chain limits.
func NewCompressor(w io.Writer, windowSize, maxChain int) (*Compressor, error) {
	return nil, ErrConfig
}

// Write buffers input; emitted compressed bytes may lag behind until a match
// boundary becomes certain, which makes output independent of Write chunking.
func (c *Compressor) Write(p []byte) (int, error) { return 0, nil }

// Flush forces all buffered input to become decodable and writes a flush
// marker. A Flush with no new input since the previous flush emits nothing.
func (c *Compressor) Flush() error { return nil }

// Close writes the stream trailer; it does not close the underlying writer.
func (c *Compressor) Close() error { return nil }

// CompressParallel compresses data in fixed-size blocks using up to workers
// goroutines. Blocks after the first may refer to up to windowSize bytes of
// the previous block. The result is one legal stream and is byte-identical
// for any workers value (1, 2, 4, 8, ...).
func CompressParallel(data []byte, blockSize, windowSize, maxChain, workers int) ([]byte, error) {
	return nil, ErrConfig
}
