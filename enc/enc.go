package enc

import "errors"

var (
	ErrConfig   = errors.New("enc: invalid configuration")
	ErrClosed   = errors.New("enc: writer closed")
)

type Options struct {
	WindowCap  int
	ChainLimit int
}

type Writer struct{}

func NewWriter(opts Options) (*Writer, error) { return nil, nil }
func (w *Writer) Write(p []byte) (int, error) { return 0, nil }
func (w *Writer) Flush() error { return nil }
func (w *Writer) Close() error { return nil }
func (w *Writer) Output() []byte { return nil }

func Compress(data []byte, opts Options) ([]byte, error) { return nil, nil }
func CompressParallel(data []byte, blockSize, workers int, opts Options) ([]byte, error) { return nil, nil }
