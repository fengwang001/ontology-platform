// Package golomb encodes a single non-negative integer with a Golomb code
// (a Rice code when M is a power of two). It depends on no other package.
//
// For n: q = n/M, r = n-q*M; the quotient is written in unary (q ones
// followed by a zero), the remainder in truncated binary: b=ceil(log2 M),
// lim=2^b-M; r<lim uses b-1 bits for r, otherwise b bits for r+lim.
package golomb

import "errors"

// Sentinel errors. They are mutually distinct and usable with errors.Is.
var (
	ErrInvalidParam = errors.New("golomb: M must be positive")
	ErrNegative     = errors.New("golomb: value must be non-negative")
	ErrTruncated    = errors.New("golomb: bit stream ended in the middle of a code")
)

// BitReader supplies one bit (0 or 1) at a time, most significant bit first.
type BitReader interface {
	ReadBit() (int, error)
}

// Codec holds the precomputed coding parameters for a fixed M.
type Codec struct {
	M   int64
	b   int
	lim uint64
}

// New returns a codec for M; M <= 0 is rejected with ErrInvalidParam.
func New(M int) (*Codec, error) {
	if M <= 0 {
		return nil, ErrInvalidParam
	}
	m := int64(M)
	b := 0
	for int64(1)<<uint(b) < m {
		b++
	}
	return &Codec{M: m, b: b, lim: uint64(int64(1)<<uint(b)) - uint64(m)}, nil
}

// EncodeOne encodes one value as an MSB-first string of '0'/'1' bits.
// A negative value is rejected with ErrNegative.
func (c *Codec) EncodeOne(n int64) (string, error) {
	if n < 0 {
		return "", ErrNegative
	}
	q := n / c.M
	r := uint64(n - q*c.M)
	s := make([]byte, 0, int(q)+1+c.b)
	for i := int64(0); i < q; i++ {
		s = append(s, '1')
	}
	s = append(s, '0') // unary terminator
	if c.b > 0 {
		if r < c.lim {
			s = appendBits(s, r, c.b-1)
		} else {
			s = appendBits(s, r+c.lim, c.b)
		}
	}
	return string(s), nil
}

// DecodeOne reads one value from r. Running out of bits mid-code returns
// whatever ReadBit returned (wrapped by the caller as ErrTruncated).
func (c *Codec) DecodeOne(r BitReader) (int64, error) {
	var q int64
	for {
		bit, err := r.ReadBit()
		if err != nil {
			return 0, err
		}
		if bit == 0 {
			break
		}
		q++
	}
	var rem uint64
	if c.b > 0 {
		var v uint64
		for i := 0; i < c.b-1; i++ {
			bit, err := r.ReadBit()
			if err != nil {
				return 0, err
			}
			v = (v << 1) | uint64(bit)
		}
		if v < c.lim { // short code word: those b-1 bits are r itself
			rem = v
		} else { // long code word: read one more bit, subtract lim
			bit, err := r.ReadBit()
			if err != nil {
				return 0, err
			}
			v = (v << 1) | uint64(bit)
			rem = v - c.lim
		}
	}
	return q*c.M + int64(rem), nil
}

// B exposes the truncated-binary width, for use by the stream layer/tests.
func (c *Codec) B() int { return c.b }

func appendBits(dst []byte, v uint64, width int) []byte {
	for i := width - 1; i >= 0; i-- {
		if v&(uint64(1)<<uint(i)) != 0 {
			dst = append(dst, '1')
		} else {
			dst = append(dst, '0')
		}
	}
	return dst
}
