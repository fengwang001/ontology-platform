package spillgrp

import (
	"fmt"
	"io"
	"os"
	"sync"

	"ontology/row"
)

type state int

const (
	building state = iota
	spilling
	sealed
)

// Seeker 抽象回退用的 Seek，便于在测试中注入失败。
type Seeker interface {
	Seek(offset int64, whence int) (int64, error)
}

// SeekFailFn 决定某次 Seek(组起点) 是否失败；nil 表示从不失败。
type SeekFailFn func(key string, attempt int) bool

// Group 是右侧单个同键组的内存缓冲与可选溢出文件。
type Group struct {
	key   string
	mem   []row.Row // 阈值内驻留行；溢出时保留前 T 行
	thr   int
	spill bool

	mu      sync.Mutex
	cond    *sync.Cond
	st      state
	f       *os.File
	start   int64 // 组起点字节偏移（== 头长）
	written int

	peak     int // 历史最大驻留行数（非导出计数器）
	rescans  int // 回退 Seek 次数
	diskRead int // 重扫从磁盘读出的行数

	failSeek SeekFailFn
}

// NewGroup 创建一个阈值为 threshold 的同键组。
	func NewGroup(key string, threshold int, failSeek SeekFailFn) *Group {
	g := &Group{key: key, thr: threshold, failSeek: failSeek}
	g.cond = sync.NewCond(&g.mu)
	return g
}

// Append 追加一行；达到阈值即惰性转为溢出模式（调用方需持构建语义）。
func (g *Group) Append(r row.Row) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.st == building && !g.spill && len(g.mem) < g.thr {
		g.mem = append(g.mem, r)
		if len(g.mem) > g.peak {
			g.peak = len(g.mem)
		}
		return nil
	}
	if err := g.beginSpillLocked(); err != nil {
		return err
	}
	if err := writeFrame(g.f, r); err != nil {
		return err
	}
	g.written++
	return nil
}

// beginSpillLocked 在首次超阈值时建临时文件、写头（nrows 占位为 0）。
func (g *Group) beginSpillLocked() error {
	if g.spill {
		return nil
	}
	f, err := os.CreateTemp("", "smj-spill-*.dat")
	if err != nil {
		return err
	}
	if err := writeHeader(f, g.key, 0); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	g.f, g.spill, g.st, g.start = f, true, spilling, int64(headerLen(g.key))
	return nil
}

// Seal 声明该组已完整：把头部 nrows 改写为实际行数并 fsync，状态转 sealed。
func (g *Group) Seal() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.spill {
		g.st = sealed
		g.cond.Broadcast()
		return nil
	}
	hdr := make([]byte, headerLen(g.key))
	copy(hdr, magic)
	hdr[8] = version
	for i := 9; i < 13; i++ {
		hdr[i] = 0
	}
	n := len(g.mem) + g.written
	putHeaderTail(hdr, g.key, n)
	if _, err := g.f.Seek(0, io.SeekStart); err != nil {
		return err
}
	if _, err := g.f.Write(hdr); err != nil {
		return err
	}
	if err := g.f.Sync(); err != nil {
		return err
	}
	g.st = sealed
	g.cond.Broadcast()
	return nil
}

// waitSealed 阻塞直到 sealed；重扫前必须先过此门（显式状态，非 sleep）。
func (g *Group) waitSealed() {
	g.mu.Lock()
	for g.st != sealed {
		g.cond.Wait()
	}
	g.mu.Unlock()
}

// Spilled 报告该组是否已溢出。
func (g *Group) Spilled() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.spill
}

// Peak 返回历史最大驻留行数（阈值上界）。
func (g *Group) Peak() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.peak
}

// DiskReads 返回重扫累计从磁盘读出的行数。
func (g *Group) DiskReads() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.diskRead
}

// Name 返回溢出文件名（未溢出时为空）。
func (g *Group) Name() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.f == nil {
		return ""
	}
	return g.f.Name()
}

// Cleanup 删除溢出文件。
func (g *Group) Cleanup() {
	g.mu.Lock()
	f := g.f
	name := ""
	if f != nil {
		name = f.Name()
	}
	g.mu.Unlock()
	if f != nil {
		f.Close()
	}
	if name != "" {
		os.Remove(name)
	}
}

// seekError 生成带键与组信息的可判定 Seek 错误。
func seekError(key string) error {
	return fmt.Errorf("%w: key=%q group-start", ErrSeek, key)
}
