package wire

import (
	"errors"
	"io"
)

const (
	TagLiteral byte = iota
	TagMatch
	TagFlush
	TagEnd
)

var Magic = [3]byte{'L', 'Z', '7'}
const Version byte = 1

var (
	ErrTruncated = errors.New("wire: truncated record")
	ErrOverflow  = errors.New("wire: varint overflow")
)

func writeAll(w io.Writer, p []byte) (int, error) {
	n, err := w.Write(p)
	if err != nil {
		return n, err
	}
	if n != len(p) {
		return n, io.ErrShortWrite
	}
	return n, nil
}

func Header(w io.Writer, windowSize, chainLimit int) (int, error) {
	var b []byte
	b = append(b, Magic[:]...)
	b = append(b, Version)
	b = AppendVarint(b, uint64(windowSize))
	b = AppendVarint(b, uint64(chainLimit))
	return writeAll(w, b)
}

func Literal(w io.Writer, p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	b := AppendVarint([]byte{TagLiteral}, uint64(len(p)))
	n, err := writeAll(w, b)
	if err != nil {
		return n, err
	}
	m, err := writeAll(w, p)
	return n + m, err
}

func Match(w io.Writer, distance, length int) (int, error) {
	b := []byte{TagMatch}
	b = AppendVarint(b, uint64(distance))
	b = AppendVarint(b, uint64(length))
	return writeAll(w, b)
}

func Flush(w io.Writer) (int, error) { return writeAll(w, []byte{TagFlush}) }

func End(w io.Writer, total int, checksum uint64) (int, error) {
	b := []byte{TagEnd}
	b = AppendVarint(b, uint64(total))
	b = AppendVarint(b, checksum)
	return writeAll(w, b)
}

func AppendVarint(p []byte, v uint64) []byte {
	for v >= 0x80 {
		p = append(p, byte(v)|0x80)
		v >>= 7
	}
	return append(p, byte(v))
}

func ReadVarint(p []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(p) && i < 10; i++ {
		c := p[i]
		if i == 9 && (c&0x80 != 0 || c > 1) {
			return 0, i + 1, ErrOverflow
		}
		v |= uint64(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			return v, i + 1, nil
		}
	}
	if len(p) >= 10 {
		return 0, 10, ErrOverflow
	}
	return 0, len(p), ErrTruncated
}
