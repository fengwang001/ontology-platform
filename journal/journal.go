package journal

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"

	"ontology/change"
)

var magic = [8]byte{'O', 'N', 'T', 'J', 'R', 'N', 'L', 1}

const headerLen = 8

var (
	// ErrHeader 头部不完整或魔数不符。
	ErrHeader = errors.New("journal: incomplete or bad header")
	// ErrLength 长度前缀不完整。
	ErrLength = errors.New("journal: incomplete length prefix")
	// ErrBody 记录体不完整。
	ErrBody = errors.New("journal: incomplete record body")
	// ErrCRC CRC 不匹配（含 CRC 尾部被截断）。
	ErrCRC = errors.New("journal: crc mismatch")
)

// Journal 是只追加的变更日志。
type Journal struct {
	f *os.File
}

// Create 在 path 创建带自描述头的新日志。
func Create(path string) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(magic[:]); err != nil {
		f.Close()
		return nil, err
	}
	return &Journal{f: f}, nil
}

// Open 打开已有日志（校验头）以便追加。
func Open(path string) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	hdr := make([]byte, headerLen)
	if _, err := io.ReadFull(f, hdr); err != nil {
		f.Close()
		return nil, ErrHeader
	}
	if [8]byte(hdr) != magic {
		f.Close()
		return nil, ErrHeader
	}
	return &Journal{f: f}, nil
}

// Append 以「长度前缀 + 记录体 + CRC32」追加一帧并落盘。
func (j *Journal) Append(c change.Change) error {
	body := c.Encode()
	frame := make([]byte, 4+len(body)+4)
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(body)))
	copy(frame[4:], body)
	binary.BigEndian.PutUint32(frame[4+len(body):], crc32.ChecksumIEEE(body))
	if _, err := j.f.Write(frame); err != nil {
		return err
	}
	return j.f.Sync()
}

// Close 关闭日志。
func (j *Journal) Close() error { return j.f.Close() }

// Path 返回底层文件路径。
func (j *Journal) Path() string { return j.f.Name() }

// Replay 读取 path 中完整帧并逐条回调。遇到尾部损坏时返回对应分类错误，
// 回调覆盖的恰是损坏帧之前的完整记录，半截帧不生效。
func Replay(path string, fn func(change.Change) error) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return Parse(data, fn)
}

// Parse 对日志字节做分类重放，便于对截断/损坏数据做纯函数测试。
func Parse(data []byte, fn func(change.Change) error) error {
	if len(data) < headerLen {
		return ErrHeader
	}
	if [8]byte(data[:headerLen]) != magic {
		return ErrHeader
	}
	pos := headerLen
	for pos < len(data) {
		if len(data)-pos < 4 {
			return ErrLength
		}
		n := int(binary.BigEndian.Uint32(data[pos : pos+4]))
		bodyStart := pos + 4
		bodyEnd := bodyStart + n
		if len(data) < bodyEnd {
			return ErrBody
		}
		crcEnd := bodyEnd + 4
		if len(data) < crcEnd {
			return ErrCRC // CRC 尾部被截断
		}
		body := data[bodyStart:bodyEnd]
		want := binary.BigEndian.Uint32(data[bodyEnd:crcEnd])
		if crc32.ChecksumIEEE(body) != want {
			return ErrCRC
		}
		c, err := change.Decode(body)
		if err != nil {
			return ErrCRC
		}
		if err := fn(c); err != nil {
			return err
		}
		pos = crcEnd
	}
	return nil
}
