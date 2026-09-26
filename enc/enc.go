// Package enc is a streaming LZ77 compressor in the wire format.
//
// A single Encoder is not safe for concurrent use; use separate encoders.
package enc

import (
	"errors"
	"hash"
	"hash/fnv"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

var ErrBlockSize = errors.New("enc: blockSize must be > 0")

const (
	defaultWindowCap  = 1 << 16
	defaultChainLimit = 32
)

type config struct {
	windowCap  int
	chainLimit int
}

type Option func(*config)

func WithWindow(n int) Option     { return func(c *config) { c.windowCap = n } }
func WithChainLimit(n int) Option { return func(c *config) { c.chainLimit = n } }

// Encoder buffers undecided bytes so output never depends on Write sizes.
type Encoder struct {
	cfg     config
	out     []byte
	pend    []byte
	win     *window.Window
	mt      *match.Matcher
	closed  bool
	flushed bool
	total   int
	sum     hash.Hash64
}

func New(opts ...Option) (*Encoder, error) {
	c := config{defaultWindowCap, defaultChainLimit}
	for _, o := range opts {
		o(&c)
	}
	win, err := window.New(c.windowCap)
	if err != nil {
		return nil, err
	}
	mt, err := match.New(win, c.chainLimit)
	if err != nil {
		return nil, err
	}
	e := &Encoder{cfg: c, win: win, mt: mt, sum: fnv.New64a()}
	e.out = wire.AppendHeader(e.out, c.windowCap, c.chainLimit)
	return e, nil
}

func (e *Encoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, errors.New("enc: write after close")
	}
	e.pend = append(e.pend, p...)
	e.mt.Append(p)
	e.flushed = false
	e.encode(false)
	return len(p), nil
}

// encode greedily decides records and returns how many pending bytes became
// permanent. Non-drain runs leave bytes whose match might still extend with
// future Write data; drain (Flush/Close) forces every match to end now.
// encode greedily decides records. Non-drain runs keep the trailing bytes
// that could still extend a match with future Write data pending.
func (e *Encoder) encode(drain bool) int {
	p := e.pend
	i, litStart, consumed := 0, 0, 0
	for i < len(p) {
		l, d := e.mt.Look(p, i)
		if l >= 3 && (drain || i+l < len(p)) {
			if i > litStart {
				e.out = wire.AppendLit(e.out, p[litStart:i])
			}
			e.out = wire.AppendMatch(e.out, d, l)
			e.mt.Commit(litStart, i+l-litStart, e.win.Len())
			consumed = i + l
			i += l
			litStart = i
			continue
		}
		if !drain && i+2 >= len(p) {
			break
		}
		e.mt.Commit(i, 1, e.win.Len())
		consumed = i + 1
		i++
	}
	if i > litStart {
		e.out = wire.AppendLit(e.out, p[litStart:i])
	}
	e.sum.Write(p[:consumed])
	e.total += consumed
	e.pend = append(e.pend[:0], p[consumed:]...)
	return consumed
}

func (e *Encoder) Flush() error {
	if e.closed {
		return errors.New("enc: flush after close")
	}
	if e.flushed {
		return nil
	}
	e.encode(true)
	e.out = wire.AppendFlush(e.out)
	e.flushed = true
	return nil
}

func (e *Encoder) Close() error {
	if e.closed {
		return nil
	}
	e.encode(true)
	e.out = wire.AppendEnd(e.out, e.total, e.sum.Sum64())
	e.closed = true
	return nil
}

func (e *Encoder) Bytes() []byte { return e.out }

func Compress(data []byte, opts ...Option) ([]byte, error) {
	e, err := New(opts...)
	if err != nil {
		return nil, err
	}
	if _, err := e.Write(data); err != nil {
		return nil, err
	}
	if err := e.Close(); err != nil {
		return nil, err
	}
	return e.Bytes(), nil
}
