package norm

import (
	"errors"
	"ontology/eol"
	"ontology/span"
	"ontology/ws"
	"strconv"
)

type EndingPolicy int

type Config struct {
	Ending                    EndingPolicy
	StrictNUL                 bool
	PendingLimit, OutputLimit int
}

var (
	ErrNUL          = errors.New("norm: NUL byte")
	ErrPendingLimit = errors.New("norm: pending whitespace limit")
	ErrOutputLimit  = errors.New("norm: output limit")
	ErrClosed       = errors.New("norm: normalizer closed")
)

type OffsetError struct {
	Op     string
	Offset int
	Err    error
}

func (e *OffsetError) Error() string {
	return e.Op + ": offset " + strconv.Itoa(e.Offset) + ": " + e.Err.Error()
}
func (e *OffsetError) Unwrap() error { return e.Err }

type Normalizer struct {
	cfg                Config
	dec                eol.Decoder
	pending            ws.Buffer
	m                  *span.Map
	out                []byte
	op, ip             int
	closed, failed, cr bool
}

func New(c Config) *Normalizer {
	if c.PendingLimit == 0 {
		c.PendingLimit = 1 << 20
	}
	return &Normalizer{cfg: c, m: span.NewMap()}
}
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.closed || n.failed {
		return 0, n.closedErr()
	}
	for i, b := range p {
		if e := n.feed(b); e != nil {
			return i, e
		}
	}
	return len(p), nil
}
func (n *Normalizer) feed(b byte) error {
	if n.cfg.StrictNUL && b == 0 {
		n.failed = true
		return n.err(ErrNUL)
	}
	if n.cr {
		n.cr = false
		if b == '\n' {
			n.ip++
			return n.newline(n.ip - 2)
		}
		if e := n.newline(n.ip - 1); e != nil {
			return e
		}
	}
	switch {
	case b == '\r':
		n.cr = true
		n.ip++
		return nil
	case b == '\n':
		n.ip++
		return n.newline(n.ip - 1)
	case ws.IsSpace(b):
		if n.pending.Len() >= n.cfg.PendingLimit {
			n.failed = true
			return n.err(ErrPendingLimit)
		}
		n.pending.Add(b)
	default:
		if e := n.flush(); e != nil {
			return e
		}
		if e := n.copyByte(b, n.ip); e != nil {
			return e
		}
	}
	n.ip++
	return nil
}
func (n *Normalizer) newline(start int) error {
	if d := n.pending.Take(); len(d) > 0 {
		n.m.Add(span.Delete, n.ip-len(d), n.ip, n.op, n.op)
	}
	return n.copyByte('\n', start)
}
func (n *Normalizer) flush() error {
	if d := n.pending.Take(); len(d) > 0 {
		return n.copy(d, n.ip-len(d))
	}
	return nil
}
func (n *Normalizer) copyByte(b byte, start int) error { return n.copy([]byte{b}, start) }
func (n *Normalizer) copy(p []byte, start int) error {
	if n.cfg.OutputLimit > 0 && n.op+len(p) > n.cfg.OutputLimit {
		n.failed = true
		return n.err(ErrOutputLimit)
	}
	n.m.Add(span.Copy, start, start+len(p), n.op, n.op+len(p))
	n.out, n.op = append(n.out, p...), n.op+len(p)
	return nil
}
func (n *Normalizer) Close() error {
	if n.closed || n.failed {
		return n.closedErr()
	}
	if n.cr {
		if e := n.newline(n.ip - 1); e != nil {
			n.failed = true
			return e
		}
	}
	if d := n.pending.Take(); len(d) > 0 {
		n.m.Add(span.Delete, n.ip-len(d), n.ip, n.op, n.op)
	}
	if e := n.policy(); e != nil {
		n.failed = true
		return e
	}
	n.closed = true
	return nil
}
func (n *Normalizer) policy() error {
	if n.cfg.Ending == EnsureOne {
		for len(n.out) > 1 && n.out[len(n.out)-1] == '\n' && n.out[len(n.out)-2] == '\n' {
			n.out = n.out[:len(n.out)-1]
		}
		if len(n.out) > 0 && n.out[len(n.out)-1] != '\n' {
			if e := n.spaceFor(); e != nil {
				return e
			}
			n.out = append(n.out, '\n')
		}
	}
	if n.cfg.Ending == TrimTrailing {
		for len(n.out) > 0 && n.out[len(n.out)-1] == '\n' {
			n.out = n.out[:len(n.out)-1]
		}
		if len(n.out) > 0 {
			if e := n.spaceFor(); e != nil {
				return e
			}
			n.out = append(n.out, '\n')
		}
	}
	n.op = len(n.out)
	n.m.Finish(n.ip, n.op)
	return nil
}
func (n *Normalizer) spaceFor() error {
	if n.cfg.OutputLimit > 0 && n.op+1 > n.cfg.OutputLimit {
		n.failed = true
		return n.err(ErrOutputLimit)
	}
	return nil
}
func (n *Normalizer) Output() []byte    { return append([]byte(nil), n.out...) }
func (n *Normalizer) Map() *span.Map    { return n.m }
func (n *Normalizer) err(e error) error { return &OffsetError{"write", n.ip, e} }
func (n *Normalizer) closedErr() error  { return &OffsetError{"close", n.ip, ErrClosed} }
