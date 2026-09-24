// Package journal 提供变更日志的追加写、同步与截断可分类重放。
package journal

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"

	"ontology/change"
)

var (
	// ErrShortHeader：文件头不完整。
	ErrShortHeader = errors.New("journal: incomplete header")
	// ErrBadHeader：文件头魔数/版本不匹配。
	ErrBadHeader = errors.New("journal: bad header")
	// ErrShortLength：长度前缀不完整。
	ErrShortLength = errors.New("journal: incomplete length prefix")
	// ErrShortRecord：记录体（含 CRC）不完整。
	ErrShortRecord = errors.New("journal: incomplete record body")
	// ErrCRC：CRC32 不匹配。
	ErrCRC = errors.New("journal: crc mismatch")
)

const (
	magic      = "ONTWAL"
	headerSize = 9 // magic(6) + version(1) + flags(1)
	version    = 1
	crcSize    = 4
)

var header = []byte(magic + string([]byte{version, 0}))

// Journal 是一个可追加的变更日志文件。
type Journal struct {
	f *os.File
}

// Create 创建（或截断重建）日志文件并写入自描述头。
func Create(path string) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(header); err != nil {
		f.Close()
		return nil, err
	}
	return &Journal{f: f}, nil
}

// Open 打开已存在的日志文件用于追加。
func Open(path string) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	return &Journal{f: f}, nil
}

// Append 将一条变更以 [长度][记录体][CRC32] 帧追加并 fsync。
func (j *Journal) Append(c change.Change) error {
	body, err := c.Encode()
	if err != nil {
		return err
	}
	var frame [4]byte
	binary.BigEndian.PutUint32(frame[:], uint32(len(body)))
	if _, err := j.f.Write(frame[:]); err != nil {
		return err
	}
	if _, err := j.f.Write(body); err != nil {
		return err
	}
	var cs [4]byte
	binary.BigEndian.PutUint32(cs[:], crc32.ChecksumIEEE(body))
	if _, err := j.f.Write(cs[:]); err != nil {
		return err
	}
	return j.f.Sync()
}

// Close 关闭日志文件。
func (j *Journal) Close() error { return j.f.Close() }

// Replay 重放日志：逐条返回 CRC 校验通过的变更。
// 返回 io.EOF 或四类截断/损坏错误之一；已通过的帧不受末尾坏帧影响。
func Replay(path string, fn func(change.Change) error) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) < headerSize {
		return ErrShortHeader
	}
	if string(data[:len(magic)]) != magic || data[6] != version {
		return ErrBadHeader
	}
	off := headerSize
	for off < len(data) {
		if len(data)-off < 4 {
			return ErrShortLength
		}
		n := int(binary.BigEndian.Uint32(data[off:]))
		off += 4
		if len(data)-off < n+crcSize {
			return ErrShortRecord
		}
		body := data[off : off+n]
		want := binary.BigEndian.Uint32(data[off+n : off+n+crcSize])
		if crc32.ChecksumIEEE(body) != want {
			return ErrCRC
		}
		c, err := change.Decode(body)
		if err != nil {
			return err
		}
		if err := fn(c); err != nil {
			return err
		}
		off += n + crcSize
	}
	return nil
}
