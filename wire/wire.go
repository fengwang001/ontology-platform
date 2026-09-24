package wire

import (
	"errors"
	"fmt"
)

// 记录类型标签。
const (
	TagFlush byte = 0x80
	TagEnd   byte = 0xC0
)

var (
	ErrBadMagic     = errors.New("wire: bad magic")
	ErrBadVersion   = errors.New("wire: bad version")
	ErrTruncated    = errors.New("wire: truncated stream")
	ErrBadVarint    = errors.New("wire: varint overflow or too long")
	ErrBadTag       = errors.New("wire: illegal tag byte")
	ErrZeroDistance = errors.New("wire: zero back-reference distance")
	ErrTrailing     = errors.New("wire: trailing bytes after end record")
)

// OffsetError 携带错误发生时的压缩流字节偏移。
type OffsetError struct {
	Offset int
	Err    error
}

func (e *OffsetError) Error() string {
	return fmt.Sprintf("wire: %v at byte offset %d", e.Err, e.Offset)
}

func (e *OffsetError) Unwrap() error { return e.Err }

// PutUvarint 写 LE Base128；调用方需保证容量（最多 10 字节）。
func PutUvarint(b []byte, v uint64) int {
	n := 0
	for v >= 0x80 {
		b[n] = byte(v) | 0x80
		v >>= 7
		n++
	}
	b[n] = byte(v)
	return n + 1
}

// AppendHeader 写流头。
func AppendHeader(b []byte, winCap, chainCap int) []byte {
	b = append(b, 'L', 'Z', 'X', '1')
	var tmp [10]byte
	b = append(b, tmp[:PutUvarint(tmp[:], uint64(winCap))]...)
	b = append(b, tmp[:PutUvarint(tmp[:], uint64(chainCap))]...)
	return b
}

// AppendLiteral 写一段字面量（1..63 字节）。
func AppendLiteral(b, data []byte) []byte {
	if len(data) == 0 || len(data) > 63 {
		panic("wire: literal run length must be 1..63")
	}
	return append(append(b, 0x80|byte(len(data))), data...)
}

// AppendMatch 写一条回指（dist>=1, length>=1）。
func AppendMatch(b []byte, dist, length int) []byte {
	var tmp [10]byte
	if length <= 63 {
		b = append(b, byte(length))
	} else {
		b = append(b, 0x40)
		b = append(b, tmp[:PutUvarint(tmp[:], uint64(length-64))]...)
	}
	return append(b, tmp[:PutUvarint(tmp[:], uint64(dist))]...)
}

// AppendFlush 写刷新标记。
func AppendFlush(b []byte) []byte { return append(b, TagFlush) }

// AppendEnd 写流尾。
func AppendEnd(b []byte, origLen int, crc uint32) []byte {
	var tmp [10]byte
	b = append(b, TagEnd)
	b = append(b, tmp[:PutUvarint(tmp[:], uint64(origLen))]...)
	return append(b, tmp[:PutUvarint(tmp[:], uint64(crc))]...)
}

// Record 是解码出的一条流记录。Kind: H/L/M/F/E。
type Record struct {
	Kind          byte
	Data          []byte
	Dist, Length  int
	WinCap, Chain int
	OrigN         int
	CRC           uint32
}

// Reader 增量读取记录；输入可跨任意 Feed 边界。
// Next 返回 (rec, ended, err)：数据不足时返回零值、false、nil。
// ended=true 后再次调用，若还有未消费字节则报 ErrTrailing。
type Reader struct {
	buf      []byte
	consumed int
	header   bool
	ended    bool
}

func NewReader() *Reader { return &Reader{} }

func (r *Reader) Feed(p []byte) { r.buf = append(r.buf, p...) }

func (r *Reader) fail(err error) (Record, bool, error) {
	return Record{}, false, &OffsetError{Offset: r.consumed, Err: err}
}

func readVarint(buf []byte) (v uint64, n int, short bool, err error) {
	for i := 0; i < len(buf); i++ {
		c := buf[i]
		if i == 9 && c > 1 {
			return 0, 10, false, ErrBadVarint
		}
		v |= uint64(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			return v, i + 1, false, nil
		}
		if i == 9 {
			return 0, 10, false, ErrBadVarint
		}
	}
	return 0, len(buf), true, nil
}

func (r *Reader) varint() (uint64, bool, error) {
	v, n, short, err := readVarint(r.buf)
	r.consumed += n
	r.buf = r.buf[n:]
	if err != nil {
		return 0, false, err
	}
	return v, short, nil
}

func (r *Reader) Next() (Record, bool, error) {
	if r.ended {
		if len(r.buf) > 0 {
			return r.fail(ErrTrailing)
		}
		return Record{Kind: 'E'}, true, nil
	}
	if !r.header {
		if len(r.buf) < 4 {
			return Record{}, false, nil
		}
		if string(r.buf[:3]) != "LZX" {
			return r.fail(ErrBadMagic)
		}
		if r.buf[3] != '1' {
			return Record{Kind: 0}, false, &OffsetError{Offset: 3, Err: ErrBadVersion}
		}
		r.buf = r.buf[4:]
		r.consumed += 4
		win, short, err := r.varint()
		if err != nil || short {
			if err != nil {
				return r.fail(err)
			}
			return Record{}, false, nil
		}
		chain, short, err := r.varint()
		if err != nil {
			return r.fail(err)
		}
		if short {
			return Record{}, false, nil
		}
		r.header = true
		return Record{Kind: 'H', WinCap: int(win), Chain: int(chain)}, false, nil
	}
	if len(r.buf) == 0 {
		return Record{}, false, nil
	}
	tag := r.buf[0]
	switch {
	case tag == TagFlush:
		r.buf = r.buf[1:]
		r.consumed++
		return Record{Kind: 'F'}, false, nil
	case tag == TagEnd:
		r.buf = r.buf[1:]
		r.consumed++
		on, short, err := r.varint()
		if err != nil {
			return r.fail(err)
		}
		if short {
			return Record{}, false, nil
		}
		crc, short, err := r.varint()
		if err != nil {
			return r.fail(err)
		}
		if short {
			return Record{}, false, nil
		}
		r.ended = true
		return Record{Kind: 'E', OrigN: int(on), CRC: uint32(crc)}, true, nil
	case tag&0xc0 == 0x80:
		n := int(tag & 0x3f)
		if n == 0 {
			return r.fail(ErrBadTag)
		}
		if len(r.buf) < 1+n {
			return Record{}, false, nil
		}
		data := append([]byte(nil), r.buf[1:1+n]...)
		r.buf = r.buf[1+n:]
		r.consumed += 1 + n
		return Record{Kind: 'L', Data: data}, false, nil
	case tag&0x80 == 0:
		r.buf = r.buf[1:]
		r.consumed++
		length := int(tag & 0x3f)
		if tag&0x40 != 0 {
			ext, short, err := r.varint()
			if err != nil {
				return r.fail(err)
			}
			if short {
				return Record{}, false, nil
			}
			length = 64 + int(ext)
		} else if length == 0 {
			return r.fail(ErrBadTag)
		}
		dv, short, err := r.varint()
		if err != nil {
			return r.fail(err)
		}
		if short {
			return Record{}, false, nil
		}
		if dv == 0 {
			return r.fail(ErrZeroDistance)
		}
		return Record{Kind: 'M', Dist: int(dv), Length: length}, false, nil
	default:
		return r.fail(ErrBadTag)
	}
}
