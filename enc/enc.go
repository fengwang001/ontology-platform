package enc

import (
	"errors"
	"io"

	"ontology/match"
	"ontology/window"
)

var (
	ErrInvalidWindow     = match.ErrInvalidWindow
	ErrInvalidChainLimit = match.ErrInvalidChainLimit
	ErrInvalidBlockSize  = errors.New("enc: block size must be positive")
	ErrInvalidWorkers    = errors.New("enc: workers must be positive")
	ErrClosed            = errors.New("enc: compressor is closed")
)

type Config struct {
	WindowSize int
	ChainLimit int
	MaxMatch   int
}

type Writer struct{}

func NewWriter(w io.Writer, cfg Config) (*Writer, error) { return nil, nil }

func (w *Writer) Write(p []byte) (int, error) { return 0, nil }

func (w *Writer) Flush() error { return nil }

func (w *Writer) Close() error { return nil }

func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) { return nil, nil }

var _ = window.Window{}
