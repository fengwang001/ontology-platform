// Package enc is a streaming LZ77 compressor. A single Encoder is not safe
// for concurrent use; use CompressParallel for parallel block compression.
package enc

import (
	"hash/crc32"
	"io"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

const (
	DefaultWindow = 1 << 16
	DefaultChain  = 64
	maxMatchLen   = 1 << 20
	literalBurst  = 1 << 16
)

type Config struct {
	WindowCap   int
	ChainLimit  int
}

type Encoder struct {
	out   io.Writer
	w     *window.Window
	m     *match.Matcher
	crc   uint32
	total int
	pos    int // first un-emitted absolute position
	ps     int // start of the pending undecided match
	pd     int // distance of the pending match (0 = none)
	pl     int // length of the pending match
	closed bool
	err    error
}

func New(out io.Writer, cfg Config) (*Encoder, error) {
	if cfg.WindowCap == 0 {
		cfg.WindowCap = DefaultWindow
	}
	if cfg.ChainLimit == 0 {
		cfg.ChainLimit = DefaultChain
	}
	w, err := window.New(cfg.WindowCap)
	if err != nil {
		return nil, err
	}
	m, err := match.New(w, cfg.ChainLimit)
	if err != nil {
		return nil, err
	}
	e := &Encoder{out: out, w: w, m: m, crc: crc32.ChecksumIEEE(nil)}
	if _, err := out.Write(wire.AppendHeader(nil, cfg.WindowCap, cfg.ChainLimit)); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *Encoder) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	e.w.Append(p)
	e.crc = crc32.Update(e.crc, crc32.IEEETable, p)
	e.total += len(p)
	if err := e.advance(false); err != nil {
		e.err = err
		return 0, err
	}
	return len(p), nil
}

// emit pending literals [pos, upto), coalescing into bursts.
func (e *Encoder) emitLiterals(upto int) error {
	for e.pos < upto {
		n := upto - e.pos
		if n > literalBurst {
			n = literalBurst
		}
		b := wire.AppendLiteral(nil, e.w.Slice(e.pos, n))
		if _, err := e.out.Write(b); err != nil {
			return err
		}
		e.pos += n
	}
	return nil
}

func (e *Encoder) emitMatch(dist, length int) error {
	for length > 0 {
		n := length
		if n > maxMatchLen {
			n = maxMatchLen
		}
		if _, err := e.out.Write(wire.AppendMatch(nil, dist, n)); err != nil {
			return err
		}
		length -= n
	}
	return nil
}

// advance emits every record whose verdict cannot change with future Writes.
// pos is the first un-emitted byte; ps/pd/pl describe a match starting at ps
// whose end coincides with the previous buffer end and may grow.
func (e *Encoder) advance(final bool) error {
	end := e.w.End()
	if e.pd > 0 {
		e.pl = e.m.Extend(e.ps, end, e.pd, e.pl)
		if !final && e.ps+e.pl == end {
			return e.emitLiterals(e.ps) // literals before it are decided
		}
		if err := e.emitLiterals(e.ps); err != nil {
			return err
		}
		if err := e.emitMatch(e.pd, e.pl); err != nil {
			return err
		}
		e.pos = e.ps + e.pl
		e.pd = 0
	}
	litEnd := end
	if !final {
		litEnd = end - (match.MinMatch - 1)
	}
	for e.pos+match.MinMatch <= end {
		dist, n := e.m.Find(e.pos, end)
		if n < match.MinMatch {
			if err := e.emitLiterals(e.pos + 1); err != nil {
				return err
			}
			e.pos++
			continue
		}
		if !final && e.pos+n == end { // may extend on the next Write
			e.ps, e.pd, e.pl = e.pos, dist, n
			return nil
		}
		if err := e.emitMatch(dist, n); err != nil {
			return err
		}
		e.pos += n
	}
	return e.emitLiterals(litEnd)
}
