package change

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
)

// HeaderSize 是自描述帧头长度：magic(2)+ver(2)+payloadLen(4)+crc32(4)+flagsPad(4)=16。
const HeaderSize = 16

var magic = [2]byte{'O', 'N'}

var (
	// ErrBadMagic 表示文件/帧头魔数不匹配。
	ErrBadMagic = errors.New("change: bad magic")
	// ErrTruncatedHeader 表示头部不完整。
	ErrTruncatedHeader = errors.New("change: truncated header")
	// ErrTruncatedLen 表示长度前缀不完整。
	ErrTruncatedLen = errors.New("change: truncated length prefix")
	// ErrTruncatedBody 表示记录体不完整。
	ErrTruncatedBody = errors.New("change: truncated body")
	// ErrCRC 表示 CRC32 不匹配。
	ErrCRC = errors.New("change: crc mismatch")
)

func isNaN(v float64) bool { return v != v }
func floatBits(v float64) uint64 { return math.Float64bits(v) }

func putStr(b []byte, at int, s string) int {
	binary.BigEndian.PutUint16(b[at:], uint16(len(s)))
	copy(b[at+2:], s)
	return at + 2 + len(s)
}

func getStr(b []byte, at int) (string, int, error) {
	if at+2 > len(b) {
		return "", at, ErrTruncatedBody
	}
	n := int(binary.BigEndian.Uint16(b[at:]))
	if at+2+n > len(b) {
		return "", at, ErrTruncatedBody
	}
	return string(b[at+2 : at+2+n]), at + 2 + n, nil
}

// EncodePayload 编码不含帧头的 payload。
func EncodePayload(c Change) []byte {
	size := 1 + 8 + 2 + len(c.ID) + 2 + len(c.Group) + 8
	old := c.Op == Update
	if old {
		size += 2 + len(c.OldGroup) + 8
	}
	b := make([]byte, size)
	b[0] = byte(c.Op)
	if old {
		b[0] |= 1 << 3
	}
	at := 1
	binary.BigEndian.PutUint64(b[at:], c.Ver)
	at += 8
	at = putStr(b, at, c.ID)
	at = putStr(b, at, c.Group)
	binary.BigEndian.PutUint64(b[at:], math.Float64bits(c.Value))
	at += 8
	if old {
		at = putStr(b, at, c.OldGroup)
		binary.BigEndian.PutUint64(b[at:], math.Float64bits(c.OldValue))
	}
	return b
}

// DecodePayload 解码 payload。
func DecodePayload(b []byte) (Change, error) {
	var c Change
	if len(b) < 1+8+2 {
		return c, ErrTruncatedBody
	}
	old := b[0]&(1<<3) != 0
	c.Op = Op(b[0] &^ (1 << 3))
	at := 1
	c.Ver = binary.BigEndian.Uint64(b[at:])
	at += 8
	var err error
	if c.ID, at, err = getStr(b, at); err != nil {
		return c, err
	}
	if c.Group, at, err = getStr(b, at); err != nil {
		return c, err
	}
	if at+8 > len(b) {
		return c, ErrTruncatedBody
	}
	c.Value = math.Float64frombits(binary.BigEndian.Uint64(b[at:]))
	at += 8
	if old {
		if c.OldGroup, at, err = getStr(b, at); err != nil {
			return c, err
		}
		if at+8 > len(b) {
			return c, ErrTruncatedBody
		}
		c.OldValue = math.Float64frombits(binary.BigEndian.Uint64(b[at:]))
	}
	return c, nil
}

var crcTable = crc32.MakeTable(crc32.IEEE)

// EncodeFrame 返回带头的完整帧。
func EncodeFrame(c Change) []byte {
	p := EncodePayload(c)
	f := make([]byte, HeaderSize+len(p))
	copy(f[0:2], magic[:])
	binary.BigEndian.PutUint16(f[2:], 1)
	binary.BigEndian.PutUint32(f[4:], uint32(len(p)))
	binary.BigEndian.PutUint32(f[8:], crc32.Checksum(p, crcTable))
	binary.BigEndian.PutUint32(f[12:], 0)
	copy(f[HeaderSize:], p)
	return f
}

// DecodeFrameAt 从 data[off:] 解码一帧，返回变更、帧总长与错误。
func DecodeFrameAt(data []byte, off int) (Change, int, error) {
	var c Change
	if len(data)-off < 12 {
		return c, 0, ErrTruncatedHeader
	}
	if data[off] != magic[0] || data[off+1] != magic[1] {
		return c, 0, ErrBadMagic
	}
	if len(data)-off < HeaderSize {
		return c, 0, ErrTruncatedLen
	}
	n := int(binary.BigEndian.Uint32(data[off+4:]))
	want := off + HeaderSize + n
	if len(data) < want {
		return c, 0, ErrTruncatedBody
	}
	p := data[off+HeaderSize : want]
	if binary.BigEndian.Uint32(data[off+8:]) != crc32.Checksum(p, crcTable) {
		return c, 0, ErrCRC
	}
	c, err := DecodePayload(p)
	return c, want - off, err
}
