// Package journal 实现变更日志的追加写与重放。
// 格式：自描述头 8 字节 magic(4)+version(2)+reserved(2)，
// 随后逐条 len(4,小端) + body(len) + crc32(4, body 的 IEEE CRC)。
package journal

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"

	"ontology/change"
)

// HeaderLen 是自描述头长度。
const HeaderLen = 8

var magic = []byte{'O', 'J', 'R', 'N'}

// Class 是重放错误的分类。
type Class int

const (
	// ClassHeaderShort 头部不完整。
	ClassHeaderShort Class = iota
	// ClassLenShort 长度前缀不完整。
	ClassLenShort
	// ClassBodyShort 记录体不完整。
	ClassBodyShort
	// ClassCRC CRC 不完整或不匹配。
	ClassCRC
)

func (c Class) String() string {
	switch c {
	case ClassHeaderShort:
		return "头部不完整"
	case ClassLenShort:
		return "长度前缀不完整"
	case ClassBodyShort:
		return "记录体不完整"
	case ClassCRC:
		return "CRC不匹配"
	}
	return "未知"
}

// Error 是可判定的重放错误。
type Error struct {
	Class  Class
	Offset int // 出错记录的起始偏移
}

func (e *Error) Error() string {
	return fmt.Sprintf("journal: offset %d: %s", e.Offset, e.Class)
}

// Writer 追加写日志。
type Writer struct {
	f *os.File
}

// Create 截断并新建日志，写入自描述头。
func Create(path string) (*Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	hdr := make([]byte, HeaderLen)
	copy(hdr, magic)
	binary.LittleEndian.PutUint16(hdr[4:6], 1)
	if _, err := f.Write(hdr); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f}, f.Sync()
}

// Append 以追加模式打开已有日志（调用方保证有效前缀已就绪）。
func Append(path string) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Writer{f: f}, nil
}

// Log 追加一条变更并落盘。
func (w *Writer) Log(c change.Change) error {
	body := c.Encode()
	rec := make([]byte, 4+len(body)+4)
	binary.LittleEndian.PutUint32(rec[0:4], uint32(len(body)))
	copy(rec[4:], body)
	binary.LittleEndian.PutUint32(rec[4+len(body):], crc32.ChecksumIEEE(body))
	if _, err := w.f.Write(rec); err != nil {
		return err
	}
	return w.f.Sync()
}

// Close 关闭日志。
func (w *Writer) Close() error { return w.f.Close() }

// Replay 重放日志，返回完整解码的变更与有效字节数。
// 遇到截断或 CRC 错误时返回已解码前缀与可判定的 *Error。
func Replay(path string) (recs []change.Change, validBytes int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(data) < HeaderLen {
		return nil, 0, &Error{Class: ClassHeaderShort, Offset: 0}
	}
	off := HeaderLen
	for off < len(data) {
		start := off
		rest := len(data) - off
		if rest < 4 {
			return recs, start, &Error{Class: ClassLenShort, Offset: start}
		}
		n := int(binary.LittleEndian.Uint32(data[off : off+4]))
		if rest < 4+n {
			return recs, start, &Error{Class: ClassBodyShort, Offset: start}
		}
		body := data[off+4 : off+4+n]
		if rest < 4+n+4 {
			return recs, start, &Error{Class: ClassCRC, Offset: start}
		}
		want := binary.LittleEndian.Uint32(data[off+4+n : off+4+n+4])
		if crc32.ChecksumIEEE(body) != want {
			return recs, start, &Error{Class: ClassCRC, Offset: start}
		}
		c, err := change.Decode(body)
		if err != nil {
			return recs, start, &Error{Class: ClassBodyShort, Offset: start}
		}
		recs = append(recs, c)
		off += 4 + n + 4
	}
	return recs, off, nil
}
