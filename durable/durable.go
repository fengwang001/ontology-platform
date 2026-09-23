// Package durable 实现带 CRC32 校验的持久计数器：先落盘再分发，
// 崩溃恢复后绝不回退、绝不重发号。
package durable

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sync"
)

// 文件布局：4B magic | 8B 计数器值(大端) | 4B CRC32(前 12 字节)。
const (
	magic   = "ONT1"
	headLen = 4
	valLen  = 8
	crcLen  = 4
	fileLen = headLen + valLen + crcLen
)

var (
	// ErrHeaderIncomplete 表示文件头部（magic）不完整。
	ErrHeaderIncomplete = errors.New("durable: 头部不完整")
	// ErrValueIncomplete 表示计数器值字节不完整。
	ErrValueIncomplete = errors.New("durable: 值不完整")
	// ErrCRCMismatch 表示 CRC 缺失或不匹配。
	ErrCRCMismatch = errors.New("durable: CRC 不匹配")
	// ErrOverflow 表示推进会溢出 uint64 上界。
	ErrOverflow = errors.New("durable: 计数器溢出")
)

// Counter 是持久化的单调计数器，并发安全。
type Counter struct {
	mu    sync.Mutex
	path  string
	value uint64
	wrote int // 成功持久写次数（测试与 demo 观测用）

	// 故障注入点（仅测试覆盖）。
	writeFile func(string, []byte) error
	rename    func(string, string) error
}

// Open 打开 path 处的持久计数器；文件不存在时以 start 初始化。
// 文件损坏时返回可判定错误并拒绝启动，绝不从 0 重建。
func Open(path string, start uint64) (*Counter, error) {
	c := &Counter{path: path, writeFile: writeFileSync, rename: os.Rename}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		c.value = start
		if err := c.persistLocked(start); err != nil {
			return nil, err
		}
		return c, nil
	case err != nil:
		return nil, err
	}
	v, err := decode(data)
	if err != nil {
		return nil, err
	}
	c.value = v
	return c, nil
}

// Value 返回当前计数器值（下一个可租号段的起点）。
func (c *Counter) Value() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

// Writes 返回成功持久写次数。
func (c *Counter) Writes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wrote
}

// Next 原子地把计数器从 v 推进到 v+n 并先落盘，返回推进前的值 v，
// 即调用方独占号段 [v, v+n)。溢出或写失败时不改变任何状态。
func (c *Counter) Next(n uint64) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n == 0 {
		return 0, errors.New("durable: 推进量必须为正")
	}
	if c.value > math.MaxUint64-n {
		return 0, ErrOverflow
	}
	if err := c.persistLocked(c.value + n); err != nil {
		return 0, err
	}
	v := c.value
	c.value += n
	return v, nil
}

// persistLocked 编码后写临时文件再原子改名，调用方须持锁。
func (c *Counter) persistLocked(v uint64) error {
	tmp := c.path + ".tmp"
	if err := c.writeFile(tmp, encode(v)); err != nil {
		return fmt.Errorf("durable: 写临时文件失败: %w", err)
	}
	if err := c.rename(tmp, c.path); err != nil {
		return fmt.Errorf("durable: 原子改名失败: %w", err)
	}
	c.wrote++
	return nil
}

func encode(v uint64) []byte {
	buf := make([]byte, fileLen)
	copy(buf, magic)
	binary.BigEndian.PutUint64(buf[headLen:], v)
	binary.BigEndian.PutUint32(buf[headLen+valLen:], crc32.ChecksumIEEE(buf[:headLen+valLen]))
	return buf
}

func decode(data []byte) (uint64, error) {
	switch {
	case len(data) < headLen:
		return 0, ErrHeaderIncomplete
	case len(data) < headLen+valLen:
		return 0, ErrValueIncomplete
	case len(data) < fileLen:
		return 0, ErrCRCMismatch
	case string(data[:headLen]) != magic:
		return 0, ErrHeaderIncomplete
	}
	want := binary.BigEndian.Uint32(data[headLen+valLen:])
	if crc32.ChecksumIEEE(data[:headLen+valLen]) != want {
		return 0, ErrCRCMismatch
	}
	return binary.BigEndian.Uint64(data[headLen:]), nil
}

func writeFileSync(name string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
