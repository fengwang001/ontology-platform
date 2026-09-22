package pipeline

import (
	"errors"
	"io"
	"time"

	"ontology/chunker"
	"ontology/sink"
)

var (
	ErrClosed            = errors.New("pipeline closed")
	ErrChunkTooLarge     = errors.New("chunk payload too large")
	ErrBufferLimit       = errors.New("pipeline buffer limit reached")
	ErrExtensionTooLarge = errors.New("extension too large")
	ErrWouldBlock        = sink.ErrWouldBlock
)

type Config struct {
	MinChunkSize     int
	MaxChunkSize     int
	MaxBufferBytes   int
	MaxExtensionSize int
	Window           time.Duration
	Clock            chunker.Clock
}

type Pipeline struct{ target io.Writer }

func New(w io.Writer, config Config) *Pipeline { return &Pipeline{target: w} }

func (p *Pipeline) Write(data []byte) (int, error)                             { return 0, nil }
func (p *Pipeline) WriteWithExtensions([]byte, map[string]string) (int, error) { return 0, nil }
func (p *Pipeline) Advance() error                                             { return nil }
func (p *Pipeline) Close() error                                               { return nil }
