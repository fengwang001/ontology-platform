// Package sink 把记录以“自描述头 + 长度前缀帧 + CRC32”落盘并检测损坏。
package sink

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"

	"ontology/record"
)

const (
	magic      = "OLOG1\n"
	headerLen  = 47
	payloadCRC = 1
)

var (
	// ErrHeader 头部不完整或头 CRC 不匹配。
	ErrHeader = errors.New("sink: header incomplete or mismatch")
	// ErrPrefix 长度前缀不完整。
	ErrPrefix = errors.New("sink: length prefix incomplete")
	// ErrRecord 声明的记录体超出文件剩余字节。
	ErrRecord = errors.New("sink: record body incomplete")
	// ErrCorrupt 记录体完整但 CRC 缺失或不匹配。
	ErrCorrupt = errors.New("sink: crc mismatch")
)

func buildHeader(n uint64, created uint64) []byte {
	b := make([]byte, headerLen)
	copy(b[0:6], magic)
	b[6] = payloadCRC
	// b[7:11] 保留；b[11:15] 为体 CRC；b[15:47] 为自描述体。
	binary.BigEndian.PutUint64(b[15:23], n)
	binary.BigEndian.PutUint64(b[23:31], created)
	binary.BigEndian.PutUint32(b[31:35], payloadCRC)
	binary.BigEndian.PutUint32(b[11:15], crc32.ChecksumIEEE(b[15:47]))
	return b
}

// NewWriter 创建/截断文件并写入头部。
func NewWriter(path string, created uint64) (*Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := &Writer{f: f}
	if _, err := f.Write(buildHeader(0, created)); err != nil {
		_ = f.Close()
		return nil, err
	}
	return w, nil
}

// Writer 顺序写帧。
type Writer struct {
	f *os.File
	n uint64
}

// Write 编码并追加一帧。
func (w *Writer) Write(r *record.Record) error {
	b, err := r.Encode()
	if err != nil {
		return err
	}
	frame := make([]byte, 8+len(b))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(b)))
	copy(frame[4:4+len(b)], b)
	binary.BigEndian.PutUint32(frame[4+len(b):], crc32.ChecksumIEEE(b))
	if _, err := w.f.Write(frame); err != nil {
		return err
	}
	w.n++
	return nil
}

// Close 落盘关闭。
func (w *Writer) Close() error {
	if err := w.f.Sync(); err != nil {
		_ = w.f.Close()
		return err
	}
	return w.f.Close()
}

// ReadFile 读取全部可恢复记录，返回最大完整前缀及首个损坏分类错误。
func ReadFile(path string) ([]*record.Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse 解析字节，便于故障注入测试。
func Parse(data []byte) ([]*record.Record, error) {
	if len(data) < headerLen {
		return nil, ErrHeader
	}
	if string(data[0:6]) != magic {
		return nil, ErrHeader
	}
	want := binary.BigEndian.Uint32(data[11:15])
	if crc32.ChecksumIEEE(data[15:47]) != want {
		return nil, ErrHeader
	}
	var out []*record.Record
	pos := headerLen
	for pos < len(data) {
		if len(data)-pos < 4 {
			return out, ErrPrefix
		}
		n := int(binary.BigEndian.Uint32(data[pos : pos+4]))
		pos += 4
		if len(data)-pos < n {
			return out, ErrRecord
		}
		payload := data[pos : pos+n]
		pos += n
		if len(data)-pos < 4 {
			return out, fmt.Errorf("%w: crc trailer missing", ErrCorrupt)
		}
		got := binary.BigEndian.Uint32(data[pos : pos+4])
		pos += 4
		if crc32.ChecksumIEEE(payload) != got {
			return out, fmt.Errorf("%w: record %d", ErrCorrupt, len(out)+1)
		}
		r, err := record.Decode(payload)
		if err != nil {
			return out, fmt.Errorf("%w: decode: %v", ErrCorrupt, err)
		}
		out = append(out, r)
	}
	return out, nil
}
