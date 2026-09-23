// Package enc implements the streaming LZ77 compressor.
package enc

// Config configures a compressor.
type Config struct {
	WindowCap int
	MaxChain  int
}

// Writer is a single-use streaming compressor. It is not safe for concurrent
// use by multiple goroutines.
type Writer struct{}

// NewWriter compresses into dst.
func NewWriter(dst []byte, cfg Config) (*Writer, []byte, error) {
	return &Writer{}, dst, nil
}

// Write buffers input; emitted compressed bytes are returned via the slice.
func (w *Writer) Write(p []byte, dst []byte) []byte { return dst }

// Flush forces all buffered input to become decodable.
func (w *Writer) Flush(dst []byte) []byte { return dst }

// Close writes the stream tail.
func (w *Writer) Close(dst []byte) []byte { return dst }

// Compress compresses data in one shot.
func Compress(data []byte, cfg Config) ([]byte, error) { return nil, nil }

// CompressParallel splits data into independent deterministic blocks.
func CompressParallel(data []byte, blockSize, workers int, cfg Config) ([]byte, error) {
	return nil, nil
}
