package norm

import (
	"errors"
	"strconv"

	"ontology/span"
	"ontology/ws"
)

var (
	// ErrClosed is returned after the terminal state (error or Close).
	ErrClosed = errors.New("norm: write after close/error")
	// ErrNUL marks a NUL byte in strict mode.
	ErrNUL = errors.New("norm: NUL byte in strict mode")
	// ErrWSBuffer marks an over-long undecided whitespace run.
	ErrWSBuffer = ws.ErrLimit
	// ErrOutput marks exceeding the output byte limit.
	ErrOutput = errors.New("norm: output byte limit exceeded")
)

// NULError carries the original offset of the offending NUL byte.
type NULError struct{ Off int64 }

func (e *NULError) Error() string {
	return "norm: NUL byte at original offset " + strconv.FormatInt(e.Off, 10)
}
func (e *NULError) Is(t error) bool { return t == ErrNUL }

// OutputError carries the configured output limit.
type OutputError struct{ Limit int64 }

func (e *OutputError) Error() string {
	return "norm: output limit " + strconv.FormatInt(e.Limit, 10) + " exceeded"
}
func (e *OutputError) Is(t error) bool { return t == ErrOutput }

// Policy selects the final-newline behavior at Close.
type Policy int

const (
	Keep      Policy = iota // leave the ending as-is
	EnsureOne               // non-empty output ends with exactly one \n
	TrimBlank               // drop trailing blank lines, keep one \n
)

// ApplyPolicy rewrites a normalized open-ended result according to p.
// Empty input stays empty; EnsureOne appends \n only to non-empty output.
func ApplyPolicy(out []byte, m *span.Map, p Policy, max int64) ([]byte, *span.Map, error) {
	b := span.NewBuilder()
	for _, s := range m.Segs() {
		b.Add(s.I1, s.O1)
	}
	if p == Keep {
		return out, b.Build(), nil
	}
	cut := int64(-1)
	if p == EnsureOne {
		if len(out) == 0 {
			return out, b.Build(), nil
		}
		k := int64(len(out))
		for k > 0 && out[k-1] == '\n' {
			k--
		}
		cut = k
	} else {
		if len(out) == 0 {
			return out, b.Build(), nil
		}
		k := int64(len(out))
		for k > 0 && out[k-1] == '\n' {
			k--
		}
		cut = k
	}
	if cut < 0 {
		return out, b.Build(), nil
	}
	if cut < int64(len(out)) {
		b.TruncateOut(cut)
		out = out[:cut]
	}
	if max > 0 && int64(len(out))+1 > max {
		return nil, nil, &OutputError{Limit: max}
	}
	out = append(out, '\n')
	b.Add(b.ILen(), b.OLen()+1)
	return out, b.Build(), nil
}
