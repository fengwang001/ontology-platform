// Package trunc 实现两阶段截断：先写截断标记 tm，再物理删除；并负责崩溃恢复双判定。
// 依赖 log，反向依赖不允许。
package trunc

import (
	"errors"
	"sync"

	"ontology/log"
)

var (
	// ErrTruncateBeyondCP：K > cp，被移除条目中存在尚未持久化者。
	ErrTruncateBeyondCP = errors.New("trunc: K beyond persisted checkpoint")
	// ErrRecoverOverDeleted：f > tm，越删（数据已丢），报告损坏。
	ErrRecoverOverDeleted = errors.New("trunc: recovery found first offset beyond marker (over-deleted)")
)

// Truncator 在一份 log.Log 上维护截断标记 tm 与实际起始 f。
// 写截断（标记+删除）在写锁内一次完成，故并发读者只能见到截断前或截断后的整态。
type Truncator struct {
	mu sync.RWMutex
	l  *log.Log
	tm uint64 // 截断标记
	// cpReads：最近一次 Truncate 为校验 K<=cp 读取过的元数据字段个数。
	// 只读取 cp 这一个标量字段，故恒为 1，与日志长度无关。非导出，不进公开接口。
	cpReads int
}

// New 绑定热日志；初始 tm == f == 0。
func New(l *log.Log) *Truncator { return &Truncator{l: l} }

// readCP 读取持久化位点这“一个”元数据字段并计数；cp 是标量，无需遍历条目。
func (t *Truncator) readCP() int64 {
	t.cpReads++
	return t.l.CP()
}

// validate 判定 K <= cp；无 Checkpoint（cp=-1）时任何 K 都不合法。不改状态。
func (t *Truncator) validate(k uint64) error {
	t.cpReads = 0
	cp := t.readCP()
	if cp < 0 || k > uint64(cp) {
		return ErrTruncateBeyondCP
	}
	return nil
}

// Truncate 合法当且仅当 K <= cp：先落标记 tm=K（持久化），再物理删除 [0,K)。
func (t *Truncator) Truncate(k uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.validate(k); err != nil {
		return err
	}
	t.tm = k            // 阶段一：写截断标记（崩溃注入点见 Mark）
	t.l.DeleteBefore(k) // 阶段二：物理删除
	return nil
}

// Mark 只执行阶段一（写标记、不删除），复现“标记已落、物理删除前崩溃”。
func (t *Truncator) Mark(k uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.validate(k); err != nil {
		return err
	}
	t.tm = k
	return nil
}

// Recover 按重启读到的 marker 与 first 双判定收敛：
// f == tm 干净；f < tm 截断中断，补删 [f,tm)；f > tm 越删，报损坏且不动状态。
func (t *Truncator) Recover(marker, first uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if first > marker {
		return ErrRecoverOverDeleted
	}
	t.tm = marker
	t.l.DeleteBefore(marker) // first==marker 时为空操作；first<marker 时补删收敛
	return nil
}

// Read 在读锁保护下返回热日志中 offset >= from 的条目，
// 因而不可能与“标记已到 K、删除未完”的中间态交叠。
func (t *Truncator) Read(from uint64) []log.Entry {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.l.Read(from)
}

// Marker 返回截断标记 tm。
func (t *Truncator) Marker() uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.tm
}

// First 返回实际起始 offset f。
func (t *Truncator) First() uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.l.First()
}
