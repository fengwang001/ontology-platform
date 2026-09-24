// Package norm is the streaming line-ending and trailing-whitespace
// normalizer. A Normalizer is not safe for concurrent use.
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// Policy selects the final-newline behavior.
type Policy int

const (
	Keep      Policy = iota // leave the ending untouched
	EnsureOne               // non-empty output ends with exactly one \n
	TrimBlank               // drop trailing empty lines, keep one \n
)

// Config configures a Normalizer. Zero MaxWS means unlimited; zero
// MaxOut means unlimited output.
type Config struct {
	Policy Policy
	Strict bool // reject NUL bytes
	MaxWS  int  // max held trailing spaces/tabs
	MaxOut int  // max total output bytes
}

// Sentinel and typed errors. Errors.Is works for ErrClosed; the typed
// errors carry the original offset.
var ErrClosed = errors.New("norm: writer already closed or in error state")

// NULOffsetError reports a NUL byte in strict mode.
type NULOffsetError struct{ Offset int }

func (e *NULOffsetError) Error() string { return "norm: NUL byte" }

// WhitespaceLimitError reports that the held trailing whitespace exceeded MaxWS.
type WhitespaceLimitError struct{ Offset, Limit int }

func (e *WhitespaceLimitError) Error() string { return "norm: trailing whitespace buffer limit" }

// OutputLimitError reports that output exceeded MaxOut.
type OutputLimitError struct{ Offset, Limit int }

func (e *OutputLimitError) Error() string { return "norm: output size limit" }

// Normalizer normalizes a stream.
type Normalizer struct {
	cfg   Config
	dec   *eol.Decoder
	hold  ws.Hold
	out   []byte
	mapb  span.Builder
	origN int
	err   error
	dead  bool
}

// New creates a Normalizer.
func New(cfg Config) *Normalizer {
	n := &Normalizer{cfg: cfg}
	n.dec = eol.NewDecoder(n)
	return n
}

// Write feeds raw bytes. It processes the accepted prefix; on error the
// produced prefix and its mapping are retained and the normalizer enters
// the terminal state.
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.dead {
		return 0, ErrClosed
	}
	for k, b := range p {
		orig := n.origN + k
		var err error
		if n.cfg.Strict && b == 0 {
			n.dec.Flush()
			if n.hold.Active() {
				n.mapb.Delete(n.hold.Orig0(), n.hold.Len())
				n.hold.Drop()
			}
			n.err = nil
			err = &NULOffsetError{Offset: orig}
		} else {
			n.dec.Push(b, orig)
			err = n.err
			n.err = nil
		}
		if err != nil {
			n.origN += k
			n.dead = true
			return k, err
		}
	}
	n.origN += len(p)
	return len(p), nil
}

// Literal implements eol.Sink.
func (n *Normalizer) Literal(b byte, orig int) {
	if ws.IsSpace(b) {
		if n.cfg.MaxWS > 0 && n.hold.Len() >= n.cfg.MaxWS {
			n.err = &WhitespaceLimitError{Offset: orig, Limit: n.cfg.MaxWS}
			return
		}
		n.hold.Add(b, orig)
		return
	}
	n.release(orig)
	if n.err == nil {
		n.emit(b, orig)
	}
}

// LineEnd implements eol.Sink.
func (n *Normalizer) LineEnd(anchor int, deletedCR int) {
	if n.hold.Active() {
		n.mapb.Delete(n.hold.Orig0(), n.hold.Len())
		n.hold.Drop()
	}
	if deletedCR >= 0 {
		n.mapb.Delete(deletedCR, 1)
	}
	n.emit('\n', anchor)
}

func (n *Normalizer) release(orig int) {
	if !n.hold.Active() {
		return
	}
	buf, start := n.hold.Take()
	if n.cfg.MaxOut > 0 && len(n.out)+len(buf) > n.cfg.MaxOut {
		n.err = &OutputLimitError{Offset: orig, Limit: n.cfg.MaxOut}
		return
	}
	n.out = append(n.out, buf...)
	n.mapb.Keep(start, len(buf))
}

func (n *Normalizer) emit(b byte, orig int) {
	if n.cfg.MaxOut > 0 && len(n.out) >= n.cfg.MaxOut {
		n.err = &OutputLimitError{Offset: orig, Limit: n.cfg.MaxOut}
		return
	}
	n.out = append(n.out, b)
	n.mapb.Keep(orig, 1)
}

// Close resolves pending state and applies the final-newline policy.
func (n *Normalizer) Close() error {
	if n.dead {
		return ErrClosed
	}
	n.dec.Flush()
	if n.err != nil {
		n.dead = true
		err := n.err
		n.err = nil
		return err
	}
	if n.hold.Active() {
		n.mapb.Delete(n.hold.Orig0(), n.hold.Len())
		n.hold.Drop()
	}
	switch n.cfg.Policy {
	case EnsureOne:
		if len(n.out) > 0 && n.out[len(n.out)-1] != '\n' {
			n.out = append(n.out, '\n')
			n.mapb.KeepOrigless(1)
		}
	case TrimBlank:
		k := 0
		for k < len(n.out) && n.out[len(n.out)-1-k] == '\n' {
			k++
		}
		if k > 0 {
			n.out = n.out[:len(n.out)-k+1]
			n.mapb.TrimEnd(k - 1)
		}
		if len(n.out) > 0 && n.out[len(n.out)-1] != '\n' {
			n.out = append(n.out, '\n')
			n.mapb.KeepOrigless(1)
		}
	}
	n.dead = true
	return nil
}

// Output returns the normalized bytes.
func (n *Normalizer) Output() []byte { return n.out }

// Map returns the frozen bidirectional offset map.
func (n *Normalizer) Map() *span.Map { return n.mapb.Map() }

// OrigLen returns the number of original bytes accepted so far.
func (n *Normalizer) OrigLen() int { return n.origN }

// Normalize is the one-shot convenience wrapper.
func Normalize(data []byte, cfg Config) ([]byte, *span.Map, error) {
	n := New(cfg)
	if _, err := n.Write(data); err != nil {
		return n.Output(), n.Map(), err
	}
	if err := n.Close(); err != nil {
		return n.Output(), n.Map(), err
	}
	return n.Output(), n.Map(), nil
}
