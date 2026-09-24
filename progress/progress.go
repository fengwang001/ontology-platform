// Package progress 负责进度文件（已写区间 + CRC32）与批次占用锁。
package progress

import (
	"bytes"
	"errors"
	"fmt"
	"hash/crc32"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Progress 记录一个批次的导入进度。
type Progress struct {
	ID        string
	Total     int
	Committed bool
	Ivls      [][2]int // 已成功写入的半开区间 [start,end)
}

// ErrKind 区分进度文件截断/损坏的可判定类别。
type ErrKind int

const (
	ErrHeader   ErrKind = iota // 头部不完整
	ErrInterval                // 区间记录不完整
	ErrCRC                     // CRC 缺失或不匹配
)

// LoadError 是可判定的进度文件错误；加载方仍可使用返回的恢复进度。
type LoadError struct{ Kind ErrKind }

func (e *LoadError) Error() string {
	return fmt.Sprintf("progress: corrupt file (class %d)", e.Kind)
}

// ErrBusy 表示批次正在被其他导入器处理。
var ErrBusy = errors.New("progress: batch is being imported")

func Path(dir, id string) string     { return filepath.Join(dir, id+".prog") }
func lockPath(dir, id string) string { return filepath.Join(dir, id+".lock") }

// Covers 报告下标 i 是否已落在已确认区间内。
func (p *Progress) Covers(i int) bool {
	for _, iv := range p.Ivls {
		if i >= iv[0] && i < iv[1] {
			return true
		}
	}
	return false
}

// Add 把记录 i 标记为已确认；顺序写下自动合并为单区间。
func (p *Progress) Add(i int) {
	if n := len(p.Ivls); n > 0 && p.Ivls[n-1][1] == i {
		p.Ivls[n-1][1] = i + 1
		return
	}
	p.Ivls = append(p.Ivls, [2]int{i, i + 1})
}

// Save 把进度写入文件：头部 + 区间行 + 覆盖此前全部字节的 CRC32。
func Save(dir string, p *Progress) error {
	var b bytes.Buffer
	committed := 0
	if p.Committed {
		committed = 1
	}
	fmt.Fprintf(&b, "HDR %s %d %d\n", p.ID, p.Total, committed)
	for _, iv := range p.Ivls {
		fmt.Fprintf(&b, "IVL %d %d\n", iv[0], iv[1])
	}
	fmt.Fprintf(&b, "CRC %d\n", crc32.ChecksumIEEE(b.Bytes()))
	return os.WriteFile(Path(dir, p.ID), b.Bytes(), 0o644)
}

// Load 读取进度文件。文件不存在视为从头开始（非错误）；损坏时返回
// *LoadError 分类，同时给出「最大可恢复前缀」表示的进度。
func Load(dir, id string) (*Progress, error) {
	p := &Progress{ID: id}
	data, err := os.ReadFile(Path(dir, id))
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	off := 0
	next := func() (line []byte, complete bool) {
		if off >= len(data) {
			return nil, false
		}
		i := bytes.IndexByte(data[off:], '\n')
		if i < 0 {
			line = data[off:]
			off = len(data)
			return line, false
		}
		line = data[off : off+i]
		off += i + 1
		return line, true
	}
	line, ok := next()
	var committed int
	if !ok {
		return p, &LoadError{Kind: ErrHeader}
	}
	if n, err := fmt.Sscanf(string(line), "HDR %s %d %d", &p.ID, &p.Total, &committed); err != nil || n != 3 {
		return p, &LoadError{Kind: ErrHeader}
	}
	p.Committed = committed == 1
	for {
		if off >= len(data) {
			return p, &LoadError{Kind: ErrCRC}
		}
		start := off
		line, complete := next()
		if bytes.HasPrefix(line, []byte("CRC")) || bytes.HasPrefix([]byte("CRC "), line) {
			var want uint32
			if n, err := fmt.Sscanf(string(line), "CRC %d", &want); !complete || err != nil || n != 1 {
				return p, &LoadError{Kind: ErrCRC}
			}
			if crc32.ChecksumIEEE(data[:start]) != want {
				return p, &LoadError{Kind: ErrCRC}
			}
			return p, nil
		}
		var s, e int
		if n, err := fmt.Sscanf(string(line), "IVL %d %d", &s, &e); !complete || err != nil || n != 2 {
			return p, &LoadError{Kind: ErrInterval}
		}
		p.Ivls = append(p.Ivls, [2]int{s, e})
	}
}

// Acquire 占用批次锁；持有者活跃返回 ErrBusy，过期则安全接管。
// 返回释放函数。now 可注入以判定过期。
func Acquire(dir, id string, now time.Time, ttl time.Duration) (func(), error) {
	path := lockPath(dir, id)
	create := func() error {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		fmt.Fprintf(f, "%d", now.Add(ttl).UnixNano())
		return f.Close()
	}
	if err := create(); err != nil {
		raw, rerr := os.ReadFile(path)
		var exp int64
		if rerr == nil {
			if _, e := fmt.Sscanf(string(raw), "%d", &exp); e == nil && now.UnixNano() < exp {
				return nil, ErrBusy
			}
		}
		os.Remove(path)
		if err := create(); err != nil {
			return nil, ErrBusy
		}
	}
	return func() { os.Remove(path) }, nil
}
