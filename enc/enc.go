// Package enc is the streaming LZ77 compressor and parallel block compressor.
package enc

import (
	"errors"
	"hash/crc32"
	"io"

	"ontology/wire"
)

const (
	// DefaultWindow is the fixed sliding-window capacity.
	DefaultWindow = 1 << 16
	// DefaultChain is the default candidate-chain limit per position.
	DefaultChain = 64
	minMatch     = 3
)

var (
	// ErrConfig is returned for invalid compressor configuration.
	ErrConfig = errors.New("enc: window and chain limit must be positive")
	// ErrClosed is returned after Close.
	ErrClosed = errors.New("enc: writer already closed")
)

// Config configures a compressor.
type Config struct {
	WindowSize int
	ChainLimit int
}

// Writer is a streaming compressor. It is not safe for concurrent use.
type Writer struct {
	cfg    Config
	w      io.Writer
	blk    *block
	crc    uint32
	total  int64
	hdr    bool
	closed bool
}

// New returns a streaming compressor writing records to w.
func New(w io.Writer, cfg Config) (*Writer, error) {
	if cfg.WindowSize <= 0 || cfg.ChainLimit <= 0 {
		return nil, ErrConfig
	}
	return &Writer{
		cfg: cfg, w: w,
		blk: newBlock(cfg.WindowSize, cfg.ChainLimit, minMatch),
	}, nil
}

func (z *Writer) writeHeader() error {
	if z.hdr {
		return nil
	}
	_, err := z.w.Write(wire.AppendHeader(nil, uint64(z.cfg.WindowSize)))
	z.hdr = true
	return err
}

// Write compresses p. Compressed output is independent of Write segmentation.
func (z *Writer) Write(p []byte) (int, error) {
	if z.closed {
		return 0, ErrClosed
	}
	if err := z.writeHeader(); err != nil {
		return 0, err
	}
	z.crc = crc32.Update(z.crc, crc32.IEEETable, p)
	z.total += int64(len(p))
	for _, c := range p {
		z.blk.add(c)
	}
	if _, err := z.w.Write(z.blk.out); err != nil {
		return 0, err
	}
	z.blk.out = z.blk.out[:0]
	return len(p), nil
}

// Flush forces all written input to be decodable. A second Flush with no new
// input emits no bytes. Matches after Flush may still reference old data.
func (z *Writer) Flush() error {
	if z.closed {
		return ErrClosed
	}
	if err := z.writeHeader(); err != nil {
		return err
	}
	if z.blk.win.Len() == z.blk.next && len(z.blk.out) == 0 {
		return nil // everything already decided and flushed
	}
	z.blk.drain(true)
	_, err := z.w.Write(z.blk.out)
	z.blk.out = z.blk.out[:0]
	return err
}

// Close writes the trailer. It must be called exactly once.
func (z *Writer) Close() error {
	if z.closed {
		return ErrClosed
	}
	z.closed = true
	if err := z.writeHeader(); err != nil {
		return err
	}
	z.blk.drain(false)
	var rec []byte
	rec = append(rec, z.blk.out...)
	rec = wire.AppendEnd(rec, uint64(z.total), uint64(z.crc))
	_, err := z.w.Write(rec)
	return err
}

// Candidates returns total hash-chain candidates examined so far.
func (z *Writer) Candidates() int64 { return z.blk.m.Candidates() }
