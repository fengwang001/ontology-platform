package vv

import (
	"encoding/binary"
	"hash/crc32"
)

// 头部：magic(1) | kind(1) | count(4,BE)，共 6 字节。
const headerLen = 6

const (
	kindVector = 0x01
	kindEntry  = 0x02
)

func marshalVector(v Vector) []byte {
	keys := sortedKeys(v)
	buf := makeHeader(kindVector, len(keys))
	for _, k := range keys {
		buf = appendString(buf, k)
		var nb [8]byte
		binary.BigEndian.PutUint64(nb[:], v[k])
		buf = append(buf, nb[:]...)
	}
	return appendCRC(buf)
}

func makeHeader(kind byte, n int) []byte {
	buf := make([]byte, headerLen)
	buf[0] = kind
	buf[1] = kind
	binary.BigEndian.PutUint32(buf[2:6], uint32(n))
	return buf
}

func appendString(buf []byte, s string) []byte {
	var lb [2]byte
	binary.BigEndian.PutUint16(lb[:], uint16(len(s)))
	buf = append(buf, lb[:]...)
	return append(buf, s...)
}

func appendCRC(buf []byte) []byte {
	var cb [4]byte
	binary.BigEndian.PutUint32(cb[:], crc32.ChecksumIEEE(buf))
	return append(buf, cb[:]...)
}

func parseVectorBody(data []byte) (Vector, []byte, error) {
	v := Vector{}
	n := int(binary.BigEndian.Uint32(data[2:6]))
	pos := headerLen
	for i := 0; i < n; i++ {
		if pos+2 > len(data) {
			return nil, nil, ErrEntryTruncated
		}
		idLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		pos += 2
		if pos+idLen+8 > len(data) {
			return nil, nil, ErrEntryTruncated
		}
		id := string(data[pos : pos+idLen])
		pos += idLen
		v[id] = binary.BigEndian.Uint64(data[pos : pos+8])
		pos += 8
	}
	return v, data[pos:], nil
}

// DecodeVector 反序列化向量，截断点按 DESIGN.md 第 6 节分类为可判定错误。
func DecodeVector(data []byte) (Vector, error) {
	if len(data) < headerLen {
		return nil, ErrHeaderTruncated
	}
	if data[0] != kindVector {
		return nil, ErrInvalid
	}
	v, rest, err := parseVectorBody(data)
	if err != nil {
		return nil, err
	}
	return v, checkCRC(data, rest)
}

func checkCRC(full, rest []byte) error {
	if len(rest) < 4 {
		return ErrCRCTruncated
	}
	payloadEnd := len(full) - 4
	want := binary.BigEndian.Uint32(full[payloadEnd:])
	if crc32.ChecksumIEEE(full[:payloadEnd]) != want {
		return ErrCRCMismatch
	}
	return nil
}
