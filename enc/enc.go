// Package enc implements the streaming LZ77 compressor.
package enc

import (
	"io"

	"ontology/match"
	"ontology/window"
)

// Options configures a compressor.
type Options struct {
	WindowSize int
	MaxChain   int
}

// DefaultOptions are sane defaults.
var DefaultOptions = Options{WindowSize: 1 << 15, MaxChain: 64}

// Compressor is a streaming compressor; not safe for concurrent use.
type Compressor struct {
	w io.Writer
}

// NewCompressor writes a legal stream (header immediately) to w.
func NewCompressor(w io.Writer, opts Options) (*Compressor, error) { return &Compressor{}, nil }

// Write buffers/compresses p; written bytes may be delayed to stay cut-independent.
func (c *Compressor) Write(p []byte) (int, error) { return 0, nil }

// Flush forces all input so far to become decodable; idempotent when empty.
func (c *Compressor) Flush() error { return nil }

// Close writes the trailer; the compressor is unusable afterward.
func (c *Compressor) Close() error { return nil }

var _ = match.MinMatch
var _ = window.New

// CompressParallel compresses data in fixed blocks using workers goroutines.
// The result is independent of the worker count.
func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) { return nil, nil }
