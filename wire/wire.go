package wire

import (
	"errors"
	"hash/crc32"
	"io"
)

const (
	Magic0 = 'O'
	Magic1 = 'N'
	Magic2 = 'T'
	Magic3 = 'O'
	Version = 1

	TagLit   = 0x00
	TagMatch = 0x01
	TagFlush = 0x02
	TagEnd   = 0x03
)

var (
	ErrBadMagic   = errors.New("wire: bad magic")
	ErrBadVersion = errors.New("wire: bad version")
	ErrBadTag     = errors.New("wire: bad record tag")
	ErrVarintLong = errors.New("wire: varint too long")
	ErrVarintBig  = errors.New("wire: varint overflow")
)

// MaxVarintLen 是合法 varint 的最大字节数。
const MaxVarintLen = 10

var ieee = crc32.IEEETable

func CRC(b []byte) uint32 { return crc32.ChecksumIEEE(b) }

func AppendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// Reader 按字节偏移逐字节读取，供解压器定位错误。
type Reader struct {
	B   []byte
	Off int
}

func (r *Reader) Byte() (byte, error) {
	if r.Off >= len(r.B) {
		return 0, io.ErrUnexpectedEOF
	}
	b := r.B[r.Off]
	r.Off++
	return b, nil
}

func (r *Reader) Bytes(n int) ([]byte, error) {
	if n < 0 || r.Off+n > len(r.B) {
		return nil, io.ErrUnexpectedEOF
	}
	s := r.B[r.Off : r.Off+n]
	r.Off += n
	return s, nil
}

// Uvarint 限制 10 字节且不得溢出。
func (r *Reader) Uvarint() (uint64, error) {
	var x uint64
	for i := 0; i < MaxVarintLen; i++ {
		b, err := r.Byte()
		if err != nil {
			return 0, err
		}
		if i == 9 && b > 1 {
			return 0, ErrVarintBig
		}
		x |= uint64(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			return x, nil
		}
	}
	return 0, ErrVarintLong
}

func (r *Reader) Header() error {
	want := []byte{Magic0, Magic1, Magic2, Magic3, Version}
	for i, w := range want {
		b, err := r.Byte()
		if err != nil {
			return err
		}
		if b != w {
			if i < 4 {
				return ErrBadMagic
			}
			return ErrBadVersion
		}
	}
	return nil
}
