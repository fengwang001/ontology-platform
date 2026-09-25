// Package manifest 定义导出清单的序列化与反序列化。
package manifest

import (
	"encoding/binary"
	"errors"
)

// Magic 标识清单文件。
const Magic = "ONTOMANI"

const (
	headerSize = 47
	recordSize = 8 // 每块：index 4B + crc32 4B
)

// Manifest 描述一次导出。
type Manifest struct {
	SnapVersion uint64
	EntryCount  uint64
	ChunkSize   uint64
	ChunkCount  uint32
	TotalSum    uint64
	Done        bool
	ChunkCRC    []uint32
}

// Size 返回当前清单序列化后的字节数。
func (m *Manifest) Size() int {
	return headerSize + recordSize*int(m.ChunkCount)
}

// Marshal 编码清单。
func (m *Manifest) Marshal() []byte {
	buf := make([]byte, m.Size())
	copy(buf[0:8], Magic)
	binary.BigEndian.PutUint64(buf[8:16], m.SnapVersion)
	binary.BigEndian.PutUint64(buf[16:24], m.EntryCount)
	binary.BigEndian.PutUint64(buf[24:32], m.ChunkSize)
	binary.BigEndian.PutUint32(buf[32:36], m.ChunkCount)
	binary.BigEndian.PutUint64(buf[36:44], m.TotalSum)
	if m.Done {
		buf[44] = 1
	}
	binary.BigEndian.PutUint16(buf[45:47], 0xFFFF) // 头结束哨兵
	off := headerSize
	for i, crc := range m.ChunkCRC {
		binary.BigEndian.PutUint32(buf[off:off+4], uint32(i))
		binary.BigEndian.PutUint32(buf[off+4:off+8], crc)
		off += recordSize
	}
	return buf
}

// ErrIncomplete 表示清单自身被截断或损坏。
var ErrIncomplete = errors.New("manifest: incomplete or corrupt")

// Unmarshal 严格按布局解码；长度不足或字段不自洽即 ErrIncomplete。
func Unmarshal(data []byte) (*Manifest, error) {
	if len(data) < headerSize || string(data[0:8]) != Magic {
		return nil, ErrIncomplete
	}
	if binary.BigEndian.Uint16(data[44:46]) != 0xFFFF {
		return nil, ErrIncomplete
	}
	m := &Manifest{
		SnapVersion: binary.BigEndian.Uint64(data[8:16]),
		EntryCount:  binary.BigEndian.Uint64(data[16:24]),
		ChunkSize:   binary.BigEndian.Uint64(data[24:32]),
		ChunkCount:  binary.BigEndian.Uint32(data[32:36]),
		TotalSum:    binary.BigEndian.Uint64(data[36:44]),
		Done:        data[44] == 1,
	}
	want := headerSize + recordSize*int(m.ChunkCount)
	if len(data) != want {
		return nil, ErrIncomplete
	}
	m.ChunkCRC = make([]uint32, m.ChunkCount)
	off := headerSize
	for i := range m.ChunkCRC {
		idx := binary.BigEndian.Uint32(data[off : off+4])
		if idx != uint32(i) {
			return nil, ErrIncomplete
		}
		m.ChunkCRC[i] = binary.BigEndian.Uint32(data[off+4 : off+8])
		off += recordSize
	}
	return m, nil
}
