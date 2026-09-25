// Package journal 实现变更日志的追加写与重放。
// 格式：自描述头 + 逐条「长度前缀 | 记录体 | CRC32」。
package journal

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
)

var magic = [8]byte{'O', 'N', 'T', 'J', '0', '0', '0', '1'}

// HeaderSize 是文件头字节数：magic(8) + formatVersion(4)。
const HeaderSize = 12

// Class 是重放对（可能被截断的）日志的分类结论。
type Class int

const (
	ClassOK     Class = iota // 完整合法
	ClassHeader              // 头部不完整
	ClassLength              // 长度前缀不完整
	ClassRecord              // 记录体不完整
	ClassCRC                 // CRC 缺失或不匹配
)

func (c Class) String() string {
	return [...]string{"ok", "header-incomplete", "length-incomplete", "record-incomplete", "crc-mismatch"}[c]
}

var (
	ErrHeaderIncomplete = errors.New("journal: incomplete header")
	ErrLengthIncomplete = errors.New("journal: incomplete length prefix")
	ErrRecordIncomplete = errors.New("journal: incomplete record body")
	ErrCRCMismatch      = errors.New("journal: crc missing or mismatch")
	ErrBadMagic         = errors.New("journal: bad magic")
)

// Journal 是追加写的日志句柄。
type Journal struct{ f *os.File }

// Create 新建日志并写入自描述头。
func Create(path string) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	hdr := make([]byte, 0, HeaderSize)
	hdr = append(hdr, magic[:]...)
	hdr = binary.LittleEndian.AppendUint32(hdr, 1)
	if _, err := f.Write(hdr); err != nil {
		return nil, err
	}
	return &Journal{f: f}, f.Sync()
}

// OpenAppend 打开已有日志继续追加（恢复后使用）。
func OpenAppend(path string) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Journal{f: f}, nil
}

// Append 追加一条记录并落盘：len(4) | payload | crc32(4)。
func (j *Journal) Append(payload []byte) error {
	var frame [8]byte
	binary.LittleEndian.PutUint32(frame[0:4], uint32(len(payload)))
	binary.LittleEndian.PutUint32(frame[4:8], crc32.ChecksumIEEE(payload))
	if _, err := j.f.Write(frame[0:4]); err != nil {
		return err
	}
	if _, err := j.f.Write(payload); err != nil {
		return err
	}
	if _, err := j.f.Write(frame[4:8]); err != nil {
		return err
	}
	return j.f.Sync()
}

// Close 关闭句柄。
func (j *Journal) Close() error { return j.f.Close() }

// Replay 读取日志文件，返回完整记录前缀与分类结论。
func Replay(path string) ([][]byte, Class, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ClassHeader, err
	}
	return ReplayBytes(data)
}

// ReplayBytes 解析内存中的日志字节；任何截断都返回已解析的完整前缀、
// 对应分类与可判定错误，半截记录绝不出现在结果中。
func ReplayBytes(data []byte) ([][]byte, Class, error) {
	if len(data) < HeaderSize {
		return nil, ClassHeader, ErrHeaderIncomplete
	}
	if string(data[:8]) != string(magic[:]) {
		return nil, ClassHeader, ErrBadMagic
	}
	var recs [][]byte
	for off := HeaderSize; off < len(data); {
		rem := len(data) - off
		if rem < 4 {
			return recs, ClassLength, ErrLengthIncomplete
		}
		n := int(binary.LittleEndian.Uint32(data[off : off+4]))
		if rem < 4+n {
			return recs, ClassRecord, ErrRecordIncomplete
		}
		payload := data[off+4 : off+4+n]
		if rem < 4+n+4 {
			return recs, ClassCRC, ErrCRCMismatch
		}
		if crc32.ChecksumIEEE(payload) != binary.LittleEndian.Uint32(data[off+4+n:off+4+n+4]) {
			return recs, ClassCRC, ErrCRCMismatch
		}
		recs = append(recs, append([]byte(nil), payload...))
		off += 4 + n + 4
	}
	return recs, ClassOK, nil
}
