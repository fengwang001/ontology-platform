// Package durable 实现共享目录中的持久计数器：CRC32 校验、原子推进、
// flock 跨进程串行化，以及损坏文件的严格拒绝。
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
	magicByte byte = 0xA7
	recLen         = 21 // magic(1) + value(8) + reserved(8) + crc(4)
	crcOff         = 17
	fileMode       = 0o600
)

var (
	ErrCorruptHeader   = errors.New("durable: header incomplete")
	ErrCorruptValue    = errors.New("durable: value region incomplete")
	ErrCorruptCRC      = errors.New("durable: crc missing or mismatch")
	ErrInvalidLength   = errors.New("durable: lease length must be positive")
	ErrCounterOverflow = errors.New("durable: counter overflow")
	ErrWriteFailed     = errors.New("durable: atomic write failed")
)

// Counter 是目录支持的持久计数器。零值不可用，必须经 Open 构造。
type Counter struct {
	path     string
	lockPath string
	mu       sync.Mutex
	value    uint64
	lockFile *os.File
	writes   int

	// failWrites>0 时，后续原子写返回 ErrWriteFailed（测试注入）。
	failWrites int
}

// Config 描述首次启动参数。Start 仅在文件不存在时生效。
type Config struct {
	Dir   string
	Name  string
	Start uint64
}

// Open 打开（或首次创建）计数器。文件任何形式的损坏都导致错误，绝不归零。
func Open(cfg Config) (*Counter, error) {
	name := cfg.Name
	if name == "" {
		name = "counter.bin"
	}
	path := filepath.Join(cfg.Dir, name)
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, nil):
		v, err := decode(data)
		if err != nil {
			return nil, err
		}
		return newCounter(cfg.Dir, path, v)
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
			return nil, err
		}
		c, err := newCounter(cfg.Dir, path, cfg.Start)
		if err != nil {
			return nil, err
		}
		return c, c.persist(cfg.Start)
	default:
		return nil, err
	}
}

func newCounter(dir, path string, v uint64) (*Counter, error) {
	lockPath := filepath.Join(dir, "."+filepath.Base(path)+".lock")
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		return nil, err
	}
	return &Counter{path: path, lockPath: lockPath, value: v, lockFile: lf}, nil
}

// Value 返回当前持久值的快照。
func (c *Counter) Value() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

// Grab 原子租用长度为 n 的号段，返回段起点。持久化成功才返回。
func (c *Counter) Grab(n int) (uint64, error) {
	if n <= 0 {
		return 0, ErrInvalidLength
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := syscall.Flock(int(c.lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return 0, err
	}
	defer syscall.Flock(int(c.lockFile.Fd()), syscall.LOCK_UN)

	v, err := readCurrent(c.path)
	if err != nil {
		return 0, err
	}
	c.value = v
	start := c.value
	next := start + uint64(n)
	if next < start {
		return 0, ErrCounterOverflow
	}
	if err := c.persist(next); err != nil {
		return 0, err
	}
	return start, nil
}

// Close 释放锁文件。
func (c *Counter) Close() error { return c.lockFile.Close() }

func readCurrent(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return decode(data)
}

func (c *Counter) persist(v uint64) error {
	c.writes++
	if c.failWrites > 0 {
		c.failWrites--
		return ErrWriteFailed
	}
	var rec [recLen]byte
	rec[0] = magicByte
	binary.BigEndian.PutUint64(rec[1:9], v)
	binary.PutUint32(rec[crcOff:], crc32.ChecksumIEEE(rec[:crcOff]))

	tmp := c.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fileMode)
	if err != nil {
		return errors.Join(ErrWriteFailed, err)
	}
	if _, err := f.Write(rec[:]); err != nil {
		f.Close()
		return errors.Join(ErrWriteFailed, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return errors.Join(ErrWriteFailed, err)
	}
	if err := f.Close(); err != nil {
		return errors.Join(ErrWriteFailed, err)
	}
	if err := os.Rename(tmp, c.path); err != nil {
		return errors.Join(ErrWriteFailed, err)
	}
	if d, err := os.Open(filepath.Dir(c.path)); err == nil {
		err = d.Sync()
		d.Close()
		if err != nil {
			return errors.Join(ErrWriteFailed, err)
		}
	}
	c.value = v
	return nil
}

func decode(data []byte) (uint64, error) {
	switch n := len(data); {
	case n <= 8:
		return 0, fmt.Errorf("%w: %d bytes", ErrCorruptHeader, n)
	case n <= 16:
		return 0, fmt.Errorf("%w: %d bytes", ErrCorruptValue, n)
	case n < recLen:
		return 0, fmt.Errorf("%w: %d bytes", ErrCorruptCRC, n)
	case n > recLen:
		return 0, fmt.Errorf("%w: %d extra bytes", ErrCorruptValue, n)
}
	if data[0] != magicByte {
		return 0, fmt.Errorf("%w: bad magic", ErrCorruptHeader)
	}
	want := binary.BigEndian.Uint32(data[crcOff:])
	if crc32.ChecksumIEEE(data[:crcOff]) != want {
		return 0, ErrCorruptCRC
	}
	return binary.BigEndian.Uint64(data[1:9]), nil
}
