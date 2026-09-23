// Package durable 实现带 CRC32 校验的持久计数器，支持原子推进。
package durable

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sync"
)

// 文件布局：magic(8B) | value(8B 大端) | crc32(4B, 覆盖前 16B)，定长 20B。
const (
	magicLen = 8
	valueLen = 8
	crcLen   = 4
	totalLen = magicLen + valueLen + crcLen
)

var magic = [magicLen]byte{'O', 'N', 'T', 'C', 'N', 'T', 'R', '1'}

// 三类可判定的损坏错误，可用 errors.Is 区分。
var (
	ErrHeaderIncomplete = errors.New("durable: header incomplete")
	ErrValueIncomplete  = errors.New("durable: value incomplete")
	ErrCRC              = errors.New("durable: crc mismatch")
	ErrWrite            = errors.New("durable: write failed")
	ErrRegression       = errors.New("durable: counter regression")
	ErrOverflow         = errors.New("durable: counter overflows uint64")
)

// Counter 是持久化的单调计数器。
type Counter struct {
	mu    sync.Mutex
	path  string
	v     uint64
	fault error // 测试注入的写故障
}

// Open 打开 path 处的计数器；文件不存在时以 start 初始化（首次启动）。
// 文件损坏时返回分类错误，绝不回退到 0。
func Open(path string, start uint64) (*Counter, error) {
	v, err := load(path)
	if errors.Is(err, os.ErrNotExist) {
		if werr := writeAtomic(path, start, nil); werr != nil {
			return nil, werr
		}
		return &Counter{path: path, v: start}, nil
	}
	if err != nil {
		return nil, err
	}
	return &Counter{path: path, v: v}, nil
}

// Value 返回当前持久值。
func (c *Counter) Value() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.v
}

// Advance 把计数器原子推进到 to（必须大于当前值），先落盘后生效。
func (c *Counter) Advance(to uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if to <= c.v {
		return fmt.Errorf("%w: %d -> %d", ErrRegression, c.v, to)
	}
	return c.advanceLocked(to)
}

// AdvanceBy 在单次持锁内原子地把计数器推进 n 并返回推进前的值，
// 即租得号段的起点；并发调用彼此串行，号段绝不重叠。
func (c *Counter) AdvanceBy(n uint64) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n == 0 {
		return 0, fmt.Errorf("%w: zero delta", ErrRegression)
	}
	if c.v > ^uint64(0)-n {
		return 0, fmt.Errorf("%w: %d + %d", ErrOverflow, c.v, n)
	}
	start := c.v
	if err := c.advanceLocked(c.v + n); err != nil {
		return 0, err
	}
	return start, nil
}

func (c *Counter) advanceLocked(to uint64) error {
	if err := writeAtomic(c.path, to, c.fault); err != nil {
		return err
	}
	c.v = to
	return nil
}

// InjectFault 注入（或清除）写故障，仅供测试与演示。
func (c *Counter) InjectFault(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fault = err
}

// load 读取并校验持久文件，按损坏位置分类报错。
func load(path string) (uint64, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(buf) != totalLen {
		return 0, classify(len(buf))
	}
	if string(buf[:magicLen]) != string(magic[:]) {
		return 0, ErrHeaderIncomplete
	}
	crc := binary.BigEndian.Uint32(buf[magicLen+valueLen:])
	if crc32.ChecksumIEEE(buf[:magicLen+valueLen]) != crc {
		return 0, ErrCRC
	}
	return binary.BigEndian.Uint64(buf[magicLen : magicLen+valueLen]), nil
}

// classify 按截断长度给出损坏分类。
func classify(n int) error {
	switch {
	case n < magicLen:
		return fmt.Errorf("%w (%d/%d bytes)", ErrHeaderIncomplete, n, magicLen)
	case n < magicLen+valueLen:
		return fmt.Errorf("%w (%d/%d bytes)", ErrValueIncomplete, n-magicLen, valueLen)
	default:
		return fmt.Errorf("%w (%d/%d bytes)", ErrCRC, n-magicLen-valueLen, crcLen)
	}
}

// marshal 序列化计数器值。
func marshal(v uint64) []byte {
	buf := make([]byte, totalLen)
	copy(buf, magic[:])
	binary.BigEndian.PutUint64(buf[magicLen:], v)
	binary.BigEndian.PutUint32(buf[magicLen+valueLen:], crc32.ChecksumIEEE(buf[:magicLen+valueLen]))
	return buf
}

// writeAtomic 写临时文件、fsync、再原子改名，崩溃不会留下半写文件。
func writeAtomic(path string, v uint64, fault error) error {
	if fault != nil {
		return fmt.Errorf("%w: %v", ErrWrite, fault)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".counter-*.tmp")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrWrite, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(marshal(v)); err != nil {
		tmp.Close()
		return fmt.Errorf("%w: %v", ErrWrite, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("%w: %v", ErrWrite, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("%w: %v", ErrWrite, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("%w: %v", ErrWrite, err)
	}
	return nil
}
