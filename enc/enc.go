// Package enc is the streaming LZ77 compressor.
package enc

import "errors"

// ErrBadConfig is returned for invalid compressor configuration.
var ErrBadConfig = errors.New("enc: invalid configuration")

// Compressor buffers input between Flush calls and emits compressed bytes.
type Compressor struct {
	dst func([]byte)
}

// New creates a compressor writing records through sink.
func New(sink func([]byte), windowSize, chainLimit int) (*Compressor, error) {
	return &Compressor{dst: sink}, nil
}

// Write buffers input; it never depends on chunk boundaries.
func (c *Compressor) Write(p []byte) (int, error) { return len(p), nil }

// Flush forces all buffered input to become decodable and marks the point.
func (c *Compressor) Flush() error { return nil }

// Close writes the stream trailer.
func (c *Compressor) Close() error { return nil }

// CompressParallel compresses data in fixed blocks with preset dictionaries.
func CompressParallel(data []byte, blockSize, workers, windowSize, chainLimit int) ([]byte, error) {
	return nil, nil
}
