// Package journal 实现变更日志的追加写与截断可分类的重放。
//
// 磁盘布局：头部 "ONTJ" + 版本字节 1（共 5 字节），其后每条记录为
// uint32 BE 长度 + change payload + uint32 BE CRC32-IEEE(payload)。
package journal

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"

	"ontology/change"
)

var magic = [4]byte{'O', 'N', 'T', 'J'}

// 截断 / 损坏分类哨兵错误。
var (
	ErrHeaderIncomplete = errors.New("journal: header incomplete")
	ErrBadMagic         = errors.New("journal: bad magic")
	ErrLenIncomplete    = errors.New("journal: length prefix incomplete")
	ErrBodyIncomplete   = errors.New("journal: record body incomplete")
	ErrCRC              = errors.New("journal: crc missing or mismatch")
)

// Journal 是一个可追加的日志文件句柄。
type Journal struct {
	f *os.File
}

// Create 创建新日志（截断同名旧文件）并写头部。
func Create(path string) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	hdr := append(magic[:], 1)
	if _, err := f.Write(hdr); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	return &Journal{f: f}, nil
}

// Open 打开已有日志用于追加。
func Open(path string) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	return &Journal{f: f}, nil
}

// Append 编码并追加一条变更，fsync 后返回。
func (j *Journal) Append(c change.Change) error {
	payload := c.Encode()
	rec := make([]byte, 4+len(payload)+4)
	binary.BigEndian.PutUint32(rec, uint32(len(payload)))
	copy(rec[4:], payload)
	binary.BigEndian.PutUint32(rec[4+len(payload):], crc32.ChecksumIEEE(payload))
	if _, err := j.f.Write(rec); err != nil {
		return err
	}
	return j.f.Sync()
}

// Close 关闭文件。
func (j *Journal) Close() error { return j.f.Close() }

// Replay 顺序读取日志，返回全部完整记录。遇到截断/损坏时返回已完整解析的
// 前缀与分类错误；干净结束返回 io.EOF。
func Replay(path string) ([]change.Change, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse 解析一段日志字节，规则同 Replay，供截断测试在内存中使用。
func Parse(data []byte) ([]change.Change, error) {
	if len(data) < len(magic)+1 {
		return nil, ErrHeaderIncomplete
	}
	if string(data[:4]) != string(magic[:]) {
		return nil, ErrBadMagic
	}
	p := data[5:]
	var out []change.Change
	for len(p) > 0 {
		if len(p) < 4 {
			return out, ErrLenIncomplete
		}
		n := int(binary.BigEndian.Uint32(p))
		p = p[4:]
		if len(p) < n {
			return out, ErrBodyIncomplete
		}
		payload := p[:n]
		p = p[n:]
		if len(p) < 4 {
			return out, ErrCRC
		}
		want := binary.BigEndian.Uint32(p)
		p = p[4:]
		if crc32.ChecksumIEEE(payload) != want {
			return out, ErrCRC
		}
		c, err := change.Decode(payload)
		if err != nil {
			return out, err
		}
		out = append(out, c)
	}
	return out, io.EOF
}
