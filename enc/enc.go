package enc

// Package enc is the streaming LZ77 compressor and the deterministic
// parallel block compressor. A Writer is not safe for concurrent use.

import (
	"errors"
	"io"
	"sync"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

const (
	defaultWindow = 1 << 15
	defaultChain  = 64
)

// Option configures a Writer.
type Option func(*config)

type config struct {
	windowCap int
	maxChain  int
}

// WithWindow sets the window capacity (rounded up to a power of two).
func WithWindow(n int) Option { return func(c *config) { c.windowCap = n } }

// WithMaxChain sets the candidate-chain limit.
func WithMaxChain(n int) Option { return func(c *config) { c.maxChain = n } }

// Writer compresses a byte stream.
type Writer struct {
	cfg     config
	w       io.Writer
	win     *window.Window
	mat     *match.Matcher
	pending []byte // undecided input
	total   uint64
	hash    uint64
	started bool
	closed  bool
	dirty   bool // new input since the last Flush
}

// NewWriter constructs a compressor writing to w.
func NewWriter(w io.Writer, opts ...Option) (*Writer, error) {
	c := config{windowCap: defaultWindow, maxChain: defaultChain}
	for _, o := range opts {
		o(&c)
	}
	win, err := window.New(c.windowCap)
	if err != nil {
		return nil, err
}
	mat, err := match.New(win, c.maxChain)
	if err != nil {
		return nil, err
	}
	return &Writer{cfg: c, w: w, win: win, mat: mat, hash: wire.FNV64(0, nil)}, nil
}

func (z *Writer) emitHeader() error {
	if z.started {
		return nil
	}
	z.started = true
	hdr := append(wire.Magic[:], wire.Version)
	hdr = wire.AppendUvarint(hdr, uint64(z.win.Cap()))
	_, err := z.w.Write(hdr)
	return err
}

func (z *Writer) emitLiteral(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	out := wire.AppendUvarint([]byte{wire.TagLiteral}, uint64(len(b)))
	out = append(out, b...)
	_, err := z.w.Write(out)
	return err
}

// pump commits every decidable prefix of pending.
func (z *Writer) pump() error {
	for len(z.pending) > 1 {
		d, l := z.mat.Find(z.pending, wire.MaxMatch)
		if d == 0 {
			z.mat.Index(z.pending[:1])
			z.pending = z.pending[1:]
			continue
		}
		if l < wire.MaxMatch && l == len(z.pending) {
			return nil // match may grow with future bytes
		}
		lit := z.pending[: len(z.pending)-len(z.pending):len(z.pending)-len(z.pending)]
		_ = lit
		z.pending = z.pending[0:]
		return errors.New("unreachable")
	}
	return nil
}
