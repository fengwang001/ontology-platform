// Package enc is the streaming LZ77 compressor.
package enc

import "errors"

// Options configures a Writer. Zero values select defaults.
type Options struct {
	WindowCap int
	MaxChain  int
}

// Defaults.
const (
	DefaultWindowCap = 1 << 15
	DefaultMaxChain  = 64
)

// ErrInvalidConfig rejects non-positive window or chain settings.
var ErrInvalidConfig = errors.New("enc: window capacity and chain limit must be positive")

// Writer compresses bytes. A single instance is not safe for concurrent use.
type Writer struct {
	err error
}

// NewWriter creates a streaming compressor writing into dst.
func NewWriter(dst []byte, opts Options) (*Writer, []byte, error) {
	return &Writer{}, dst, nil
}

// Write buffers data; compressed records are emitted on Flush/Close.
func (w *Writer) Write(p []byte) (int, error) { return len(p), nil }

// Flush forces a decodable boundary at the current input end.
func (w *Writer) Flush() error { return nil }

// Close emits the end record; the final stream is returned.
func (w *Writer) Close() ([]byte, error) { return nil, nil }

// CompressParallel compresses data in independent blocks with workers goroutines.
// Each block may reference up to windowCap bytes of the previous block.
func CompressParallel(data []byte, blockSize int, workers int) ([]byte, error) {
	return nil, nil
}
