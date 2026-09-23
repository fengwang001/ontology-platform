// Package journal 实现变更日志的追加写与重放。
// 格式：8 字节自描述头，随后逐条 [len u32][payload][crc32 u32]。
package journal

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"

	"ontology/change"
)

// HeaderLen 是自描述头的字节数：magic(4) + 格式版本(2) + 保留(2)。
const HeaderLen = 8

var magic = []byte{'O', 'N', 'T', 'J', 1, 0, 0, 0}

// Kind 是重放失败的分类。
type Kind int

const (
	KindHeader Kind = iota + 1 // 头部不完整或魔数不符
	KindLength                 // 长度前缀不完整
	KindBody                   // 记录体不完整
	KindCRC                    // CRC 不完整或不匹配
)

func (k Kind) String() string {
	switch k {
	case KindHeader:
		return "header incomplete"
	case KindLength:
		return "length prefix incomplete"
	case KindBody:
		return "record body incomplete"
	case KindCRC:
		return "crc mismatch"
	}
	return "unknown"
}

// ReplayError 是可判定的重放错误，Offset 为出错字节位置。
type ReplayError struct {
	Kind   Kind
	Offset int64
}

func (e *ReplayError) Error() string { return "journal: " + e.Kind.String() }

// ErrBadMagic 表示文件不是本格式的日志。
var ErrBadMagic = errors.New("journal: bad magic")

// Writer 追加写日志。写盘不经缓冲，Append 返回即落盘。
type Writer struct {
	f *os.File
}

// Create 新建（截断）日志文件并写入头部。
func Create(path string) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(magic); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f}, nil
}

// OpenAppend 以追加模式打开日志；文件为空时先写入头部。
func OpenAppend(path string) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if st.Size() == 0 {
		if _, err := f.Write(magic); err != nil {
			f.Close()
			return nil, err
		}
	}
	return &Writer{f: f}, nil
}

// Append 追加一条变更记录。
func (w *Writer) Append(c change.Change) error {
	payload := make([]byte, c.EncodedLen())
	c.Encode(payload)
	var frame [8]byte
	binary.LittleEndian.PutUint32(frame[0:4], uint32(len(payload)))
	binary.LittleEndian.PutUint32(frame[4:8], crc32.ChecksumIEEE(payload))
	if _, err := w.f.Write(frame[0:4]); err != nil {
		return err
	}
	if _, err := w.f.Write(payload); err != nil {
		return err
	}
	_, err := w.f.Write(frame[4:8])
	return err
}

// Sync 将文件刷到持久介质。
func (w *Writer) Sync() error { return w.f.Sync() }

// Close 关闭文件。
func (w *Writer) Close() error { return w.f.Close() }

// Replay 顺序重放日志，返回全部完整记录与完整前缀的字节长度。
// 遇到截断或损坏时返回之前已读出的完整记录与 *ReplayError；
// 恰好落在记录边界则 err 为 nil。
func Replay(path string) ([]change.Change, int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(data) < HeaderLen {
		return nil, 0, &ReplayError{Kind: KindHeader, Offset: int64(len(data))}
	}
	if string(data[:4]) != string(magic[:4]) {
		return nil, 0, ErrBadMagic
	}
	var out []change.Change
	off := HeaderLen
	for off < len(data) {
		if len(data)-off < 4 {
			return out, int64(off), &ReplayError{Kind: KindLength, Offset: int64(off)}
		}
		n := int(binary.LittleEndian.Uint32(data[off : off+4]))
		body := data[off+4:]
		if len(body) < n {
			return out, int64(off), &ReplayError{Kind: KindBody, Offset: int64(off + 4)}
		}
		payload := body[:n]
		if len(body) < n+4 {
			return out, int64(off), &ReplayError{Kind: KindCRC, Offset: int64(off + 4 + n)}
		}
		crc := binary.LittleEndian.Uint32(body[n : n+4])
		if crc != crc32.ChecksumIEEE(payload) {
			return out, int64(off), &ReplayError{Kind: KindCRC, Offset: int64(off + 4 + n)}
		}
		c, used, err := change.Decode(payload)
		if err != nil || used != n {
			return out, int64(off), &ReplayError{Kind: KindBody, Offset: int64(off + 4)}
		}
		out = append(out, c)
		off += 4 + n + 4
	}
	return out, int64(off), nil
}
