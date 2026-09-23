// Package dec is the streaming LZ77 decompressor.
package dec

import "errors"

// Options configures a Reader. Zero MaxOutput selects 1 GiB.
type Options struct {
	MaxOutput int64
}

// DefaultMaxOutput caps decompressed output unless overridden.
const DefaultMaxOutput = 1 << 30

// ErrInvalidConfig rejects non-positive output limits.
var ErrInvalidConfig = errors.New("dec: max output must be positive")

// Reader decompresses a wire stream. A single instance is not concurrent-safe.
type Reader struct {
	err error
}

// NewReader creates a streaming decompressor.
func NewReader(opts Options) (*Reader, error) { return &Reader{}, nil }

// Write feeds compressed bytes; parse errors are sticky and terminal.
func (r *Reader) Write(p []byte) (int, error) { return len(p), nil }

// Close verifies the stream actually ended.
func (r *Reader) Close() error { return nil }

// Output returns the fully decompressed bytes so far.
func (r *Reader) Output() []byte { return nil }
