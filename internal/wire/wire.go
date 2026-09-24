package wire

import "errors"

const (
	Magic0  byte = 0x4c
	Magic1  byte = 0x5a
	Magic2  byte = 0x37
	Version byte = 0x01
)

const (
	TagLiteral byte = iota
	TagMatch
	TagFlush
	TagEnd
)

var (
	ErrConfig               = errors.New("invalid lz77 configuration")
	ErrTruncated            = errors.New("truncated lz77 stream")
	ErrHeader               = errors.New("bad lz77 stream header")
	ErrInteger              = errors.New("invalid variable-length integer")
	ErrDistanceZero         = errors.New("match distance is zero")
	ErrDistanceBeyondOutput = errors.New("match distance beyond produced output")
	ErrDistanceBeyondWindow = errors.New("match distance beyond window capacity")
	ErrLengthMismatch       = errors.New("declared length does not match output")
	ErrChecksum             = errors.New("checksum mismatch")
	ErrTrailingBytes        = errors.New("trailing bytes after stream end")
	ErrOutputLimit          = errors.New("decompressed output limit exceeded")
)

type Config struct {
	WindowCap int
	MaxChain  int
}

func (c Config) Valid() bool {
	return c.WindowCap > 0 && c.MaxChain > 0
}

type Error struct {
	Offset int
	Err    error
}

func (e Error) Error() string { return e.Err.Error() }
func (e Error) Unwrap() error { return e.Err }

func At(offset int, err error) error {
	return Error{Offset: offset, Err: err}
}

func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

func Header(c Config) []byte {
	b := []byte{Magic0, Magic1, Magic2, Version}
	b = AppendUvarint(b, uint64(c.WindowCap))
	return AppendUvarint(b, uint64(c.MaxChain))
}

type Reader struct {
	b   []byte
	pos int
}

func NewReader(b []byte) *Reader { return &Reader{b: b} }
func (r *Reader) Pos() int       { return r.pos }
func (r *Reader) EOF() bool      { return r.pos >= len(r.b) }

func (r *Reader) Header() (Config, error) {
	if len(r.b) < 4 || r.b[0] != Magic0 || r.b[1] != Magic1 || r.b[2] != Magic2 {
		return Config{}, r.fail(ErrHeader)
	}
	if r.b[3] != Version {
		return Config{}, r.fail(ErrHeader)
	}
	r.pos = 4
	w, err := r.Uvarint()
	if err != nil {
		return Config{}, err
	}
	m, err := r.Uvarint()
	if err != nil {
		return Config{}, err
	}
	if w > uint64(1<<31-1) || m > uint64(1<<31-1) {
		return Config{}, r.fail(ErrHeader)
	}
	cfg := Config{WindowCap: int(w), MaxChain: int(m)}
	if !cfg.Valid() {
		return Config{}, r.fail(ErrHeader)
	}
	return cfg, nil
}

func (r *Reader) Tag() (byte, error) {
	if r.EOF() {
		return 0, r.fail(ErrTruncated)
	}
	t := r.b[r.pos]
	if t > TagEnd {
		return 0, r.fail(ErrHeader)
	}
	r.pos++
	return t, nil
}

func (r *Reader) Uvarint() (uint64, error) {
	start := r.pos
	var v uint64
	for i := 0; i < 10; i++ {
		if r.EOF() {
			return 0, r.fail(ErrTruncated)
		}
		x := r.b[r.pos]
		r.pos++
		if i == 9 && x > 1 {
			return 0, r.fail(ErrInteger)
		}
		v |= uint64(x&0x7f) << (7 * i)
		if x < 0x80 {
			return v, nil
		}
	}
	r.pos = start
	return 0, r.fail(ErrInteger)
}

func (r *Reader) Bytes(n uint64) ([]byte, error) {
	if n > uint64(len(r.b)-r.pos) {
		return nil, r.fail(ErrTruncated)
	}
	b := r.b[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return b, nil
}

func (r *Reader) fail(err error) error { return At(r.pos, err) }
