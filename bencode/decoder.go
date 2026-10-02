package bencode

import "sync"

// Default limits applied when Options fields are left at zero.
const (
	DefaultMaxString = 16 << 20 // 16 MiB
	DefaultMaxDepth  = 128
)

// Options configures a Decoder. Zero fields fall back to the defaults.
type Options struct {
	// MaxString is the maximum allowed byte-string length.
	MaxString uint64
	// MaxDepth is the maximum nesting depth of lists/dicts. A top-level
	// list or dict has depth 1.
	MaxDepth int
}

func (o Options) withDefaults() Options {
	if o.MaxString == 0 {
		o.MaxString = DefaultMaxString
	}
	if o.MaxDepth <= 0 {
		o.MaxDepth = DefaultMaxDepth
	}
	return o
}

// Decoder is a streaming incremental bencode decoder. Feed accepts
// arbitrarily chunked input; every top-level value is delivered as soon as
// it completes. All methods are safe for concurrent use and behave as if
// executed in some serial order.
type Decoder struct {
	mu       sync.Mutex
	opts     Options
	buf      []byte
	consumed int64
	err      *Error
}

// NewDecoder creates a Decoder with the given options.
func NewDecoder(opts Options) *Decoder {
	return &Decoder{opts: opts.withDefaults()}
}

// Feed appends p to the stream and returns the values completed by this
// call. If the input violates the format, the values completed before the
// offending byte are still returned together with the *Error; afterwards
// every Feed returns ErrPoisoned and no state changes.
func (d *Decoder) Feed(p []byte) ([]*Value, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return nil, ErrPoisoned
	}
	d.buf = append(d.buf, p...)
	var out []*Value
	for {
		pr := &parser{
			buf:       d.buf,
			maxString: d.opts.MaxString,
			maxDepth:  d.opts.MaxDepth,
		}
		v, err := pr.value(1)
		if err == errIncomplete {
			break
		}
		if err != nil {
			pe := err.(*parseError)
			d.err = &Error{Reason: pe.reason, Offset: d.consumed + int64(pe.off)}
			return out, d.err
		}
		out = append(out, v)
		d.consumed += int64(pr.pos)
		d.buf = d.buf[pr.pos:]
	}
	// Compact the pending buffer when it is mostly slack.
	if cap(d.buf) > 4*len(d.buf)+64 {
		d.buf = append([]byte(nil), d.buf...)
	}
	return out, nil
}

// Consumed reports the total number of bytes that formed complete
// top-level values.
func (d *Decoder) Consumed() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.consumed
}

// Buffered reports the number of bytes held for the not-yet-complete
// current value.
func (d *Decoder) Buffered() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.buf)
}

// Err returns the sticky original error, or nil while the decoder is
// healthy.
func (d *Decoder) Err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err == nil {
		return nil
	}
	return d.err
}

// DecodeAll decodes a byte slice holding one or more complete top-level
// values. Trailing bytes that do not form a complete value are rejected
// with ErrTruncated at the offset where the incomplete value starts.
func DecodeAll(data []byte, opts Options) ([]*Value, error) {
	d := NewDecoder(opts)
	vals, err := d.Feed(data)
	if err != nil {
		return vals, err
	}
	if d.Buffered() != 0 {
		return vals, &Error{Reason: ErrTruncated, Offset: d.Consumed()}
	}
	return vals, nil
}
