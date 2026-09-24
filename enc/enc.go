package enc

import (
	"bytes"
	"errors"
	"hash/crc32"
	"io"
	"sync"

	"ontology/match"
	"ontology/wire"
)

var (
	ErrInvalidConfig = errors.New("lz77: invalid compressor config")
	ErrClosed        = errors.New("lz77: compressor is closed")
)

type Config struct {
	WindowCapacity int
	ChainLimit     int
}

type Writer struct {
	output   io.Writer
	matcher  *match.Matcher
	config   Config
	history  []byte
	pending  []byte
	size     uint64
	checksum uint32
	probes   uint64
	closed   bool
}

func NewWriter(output io.Writer, config Config) (*Writer, error) {
	matcher, err := match.New(config.WindowCapacity, config.ChainLimit)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	if _, err := output.Write(wire.AppendHeader(nil)); err != nil {
		return nil, err
	}
	return &Writer{output: output, matcher: matcher, config: config}, nil
}

func (w *Writer) Write(data []byte) (int, error) {
	if w.closed {
		return 0, ErrClosed
	}
	w.pending = append(w.pending, data...)
	w.size += uint64(len(data))
	w.checksum = crc32.Update(w.checksum, crc32.IEEETable, data)
	return len(data), nil
}

func (w *Writer) Flush() error {
	if w.closed || len(w.pending) == 0 {
		return nil
	}
	out, probes, err := compressBlock(w.history, w.pending, w.config, true)
	if err != nil {
		return err
	}
	if _, err := w.output.Write(out); err != nil {
		return err
	}
	w.history = append(w.history, w.pending...)
	if len(w.history) > w.config.WindowCapacity {
		w.history = append([]byte(nil), w.history[len(w.history)-w.config.WindowCapacity:]...)
	}
	w.pending = w.pending[:0]
	w.probes += probes
	return nil
}

func (w *Writer) Close() error {
	if w.closed {
		return ErrClosed
	}
	out, probes, err := compressBlock(w.history, w.pending, w.config, false)
	if err == nil {
		out = wire.AppendEnd(out, w.size, uint64(w.checksum))
		_, err = w.output.Write(out)
	}
	w.closed = err == nil
	w.probes += probes
	return err
}

func (w *Writer) Probes() uint64 { return w.probes }

func Compress(data []byte, config Config) ([]byte, error) {
	var out bytes.Buffer
	writer, err := NewWriter(&out, config)
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(data); err != nil {
		return nil, err
	}
	return out.Bytes(), writer.Close()
}

func CompressParallel(data []byte, blockSize, workers int, config Config) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, ErrInvalidConfig
	}
	blocks := (len(data) + blockSize - 1) / blockSize
	if blocks == 0 {
		blocks = 1
	}
	results := make([][]byte, blocks)
	jobs := make(chan int)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for block := range jobs {
				start := block * blockSize
				end := min((block+1)*blockSize, len(data))
				dictStart := max(0, start-config.WindowCapacity)
				out, _, err := compressBlock(data[dictStart:start], data[start:end], config, block < blocks-1)
				if err == nil {
					results[block] = out
				}
			}
		}()
	}
	for block := 0; block < blocks; block++ {
		jobs <- block
	}
	close(jobs)
	group.Wait()
	out := wire.AppendHeader(nil)
	for block, part := range results {
		if part == nil && (len(data) > 0 || block == 0 && len(data) == 0) {
			return nil, ErrInvalidConfig
		}
		out = append(out, part...)
	}
	return wire.AppendEnd(out, uint64(len(data)), uint64(crc32.ChecksumIEEE(data))), nil
}

func compressBlock(dict, block []byte, config Config, flush bool) ([]byte, uint64, error) {
	matcher, err := match.New(config.WindowCapacity, config.ChainLimit)
	if err != nil {
		return nil, 0, ErrInvalidConfig
	}
	matcher.Reset(dict)
	matcher.SetBlock(block)
	out, literalStart := []byte{}, 0
	for i := 0; i < len(block); {
		distance, length := matcher.Find(i)
		if length < 3 {
			i++
			continue
		}
		if literalStart < i {
			out = wire.AppendLiteral(out, block[literalStart:i])
		}
		out = wire.AppendMatch(out, uint64(distance), uint64(length))
		matcher.Insert(i+1, length-1)
		i += length
		literalStart = i
	}
	if literalStart < len(block) {
		out = wire.AppendLiteral(out, block[literalStart:])
	}
	if flush {
		out = wire.AppendFlush(out)
	}
	return out, matcher.Probes(), nil
}
