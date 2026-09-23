// Package durable 实现基于本地文件（带 CRC32）的原子持久计数器。
package durable

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

const (
	dirPerm    = 0o755
	filePerm   = 0o644
	magicLen   = 4
	verOff     = 4
	valueOff   = 4
	valueLen   = 8
	headLen    = 12
	crcLen     = 4
	totalLen   = 16
	maxUint64  = ^uint64(0)
)

var magic = [magicLen]byte{'O', 'N', 'C', '1'}

// FailHook 在每次原子写的临时文件阶段被调用；返回错误即模拟写失败。
type FailHook func() error

// Counter 是目录支持的共享持久计数器，多个实例用 flock 串行化租用。
type Counter struct {
	dir   string
	data  string
	lock  *os.File
	mu    sync.Mutex
	value uint64
	// WriteCount 记录成功的原子持久写次数（非导出测试通过同包访问）。
	WriteCount int
	failHook   FailHook
}

// Open 打开（必要时创建）dir 下的计数器；start 仅在文件不存在时生效。
// 任何文件损坏都返回错误，绝不静默回退到 0。
func Open(dir string, start uint64) (*Counter, error) {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, err
	}
	lf, err := os.OpenFile(filepath.Join(dir, "counter.lock"),
		os.O_RDWR|os.O_CREATE, filePerm)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		lf.Close()
		return nil, err
	}
	c := &Counter{dir: dir, data: filepath.Join(dir, "counter.dat"), lock: lf}
	raw, err := os.ReadFile(c.data)
	switch {
	case errors.Is(err, os.ErrNotExist):
		c.value = start
	case err != nil:
		return nil, err
	default:
		v, err := decode(raw)
		if err != nil {
			return nil, err
		}
		c.value = v
	}
	return c, nil
}

// Value 返回当前持久值。
func (c *Counter) Value() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

// SetFailHook 注入写失败钩子。
func (c *Counter) SetFailHook(h FailHook) {
	c.mu.Lock()
	c.failHook = h
	c.mu.Unlock()
}

// Close 释放文件锁。
func (c *Counter) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lock == nil {
		return nil
	}
	err := c.lock.Close()
	c.lock = nil
	return err
}

// Advance 原子地把计数器从 from 推进到 from+n，返回新区间 [from, from+n)
// 的起点。越界返回 ErrOverflow；写失败返回 ErrWriteFailed 且不改动旧文件。
func (c *Counter) Advance(n uint64) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n == 0 {
		return 0, errors.New("durable: advance by zero")
	}
	from := c.value
	if n > maxUint64-from {
		return 0, ErrOverflow
	}
	to := from + n
	if err := c.persist(to); err != nil {
		return 0, err
	}
	c.value = to
	c.WriteCount++
	return from, nil
}

func (c *Counter) persist(v uint64) error {
	buf := make([]byte, totalLen)
	copy(buf[:magicLen], magic[:])
	buf[verOff] = 1
	binary.BigEndian.PutUint64(buf[valueOff:valueOff+valueLen], v)
	sum := crc32.ChecksumIEEE(buf[:headLen])
	binary.BigEndian.PutUint32(buf[headLen:headLen+crcLen], sum)

	tmp := filepath.Join(c.dir, ".counter.tmp")
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, filePerm)
	if err != nil {
		return errors.Join(ErrWriteFailed, err)
	}
	if c.failHook != nil {
		if err := c.failHook(); err != nil {
			f.Close()
			os.Remove(tmp)
			return errors.Join(ErrWriteFailed, err)
		}
	}
	if _, err := f.Write(buf); err != nil {
		f.Close()
		os.Remove(tmp)
		return errors.Join(ErrWriteFailed, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return errors.Join(ErrWriteFailed, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return errors.Join(ErrWriteFailed, err)
	}
	if err := os.Rename(tmp, c.data); err != nil {
		os.Remove(tmp)
		return errors.Join(ErrWriteFailed, err)
	}
	if df, err := os.Open(c.dir); err == nil {
		df.Sync()
		df.Close()
	}
	return nil
}

func decode(raw []byte) (uint64, error) {
	switch n := len(raw); {
	case n < magicLen:
		return 0, ErrHeaderIncomplete
	case string(raw[:magicLen]) != string(magic[:]):
		return 0, errors.Join(ErrHeaderIncomplete, ErrBadMagic)
	case n < headLen:
		return 0, ErrValueIncomplete
	case n < totalLen:
		return 0, ErrCRCMismatch
	}
	got := binary.BigEndian.Uint32(raw[headLen : headLen+crcLen])
	if got != crc32.ChecksumIEEE(raw[:headLen]) {
		return 0, ErrCRCMismatch
	}
	if raw[verOff] != 1 {
		return 0, ErrCRCMismatch
	}
	return binary.BigEndian.Uint64(raw[valueOff : valueOff+valueLen]), nil
}
