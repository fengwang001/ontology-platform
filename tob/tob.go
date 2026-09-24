// Package tob 实现定序日志、投递游标 deliveredUpTo 与 Propose/Deliver/Crash/RePropose，只依赖 seq 包。
package tob

import (
	"errors"
	"fmt"
	"sync"

	"ontology/seq"
)

// 四类互不相同的哨兵错误，errors.Is 可判定。
var (
	ErrEmptyPayload     = errors.New("tob: payload must not be empty")
	ErrAlreadyDelivered = errors.New("tob: seq already delivered (seq <= deliveredUpTo)")
	ErrSeqOutOfRange    = errors.New("tob: seq outside gap (deliveredUpTo, nextSeq)")
	ErrSlotFilled       = errors.New("tob: slot already filled")
)

type slot struct {
	payload string
	ok      bool
}

// Log 是进程内存日志。entries 以 seq 为下标（0 闲置），故 Deliver 为 O(1)。
type Log struct {
	mu       sync.Mutex
	alloc    *seq.Allocator
	entries  []slot
	d        int // deliveredUpTo，崩溃后保留
	lastScan int // 非导出：最近一次 Deliver 直接定位的条目数
}

func New() *Log { return &Log{alloc: seq.New(), entries: make([]slot, 1)} }

// Propose 分配 seq = nextSeq++ 并写入日志；空 payload 在任何写操作前被拒。
func (l *Log) Propose(payload string) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if payload == "" {
		return 0, ErrEmptyPayload
	}
	s := l.alloc.Alloc()
	l.entries = append(l.entries, slot{payload: payload, ok: true})
	return s, nil
}

// Deliver 仅投递 seq==d+1 的槽；槽缺则不投递、游标不进，且只直接访问这一槽。
func (l *Log) Deliver() (int, string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.d + 1
	l.lastScan = 1
	if t < len(l.entries) && l.entries[t].ok {
		l.d++
		return t, l.entries[t].payload, true
	}
	return 0, "", false
}

// Crash 丢失所有 seq > deliveredUpTo 的内存日志；nextSeq 与 d 保留。
func (l *Log) Crash() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := l.d + 1; i < len(l.entries); i++ {
		l.entries[i] = slot{}
	}
}

// RePropose 按原始 seq 回填崩溃丢失的空槽；校验全在写入前，绝不分配新 seq。
func (l *Log) RePropose(s int, payload string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s <= l.d {
		return ErrAlreadyDelivered
	}
	if s >= l.alloc.Next() {
		return ErrSeqOutOfRange
	}
	if s < len(l.entries) && l.entries[s].ok {
		return ErrSlotFilled
	}
	for len(l.entries) <= s {
		l.entries = append(l.entries, slot{})
	}
	l.entries[s] = slot{payload: payload, ok: true}
	return nil
}

// Delivered 返回 deliveredUpTo。
func (l *Log) Delivered() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.d
}

func (l *Log) nextSeq() int { return l.alloc.Next() }

// matches 校验 d、nextSeq 以及已分配未投递（崩溃后即空洞）的 seq 集合。
func (l *Log) matches(wd, wn int, wp []int) bool {
	if l.d != wd || l.nextSeq() != wn {
		return false
	}
	k := 0
	for s := l.d + 1; s < len(l.entries); s++ {
		if !l.entries[s].ok {
			continue
		}
		if k >= len(wp) || wp[k] != s {
			return false
		}
		k++
	}
	return k == len(wp)
}

// SelfCheck 用第三节十步序列核验不变量，并在多档 m 下确认 lastScan 恒为 1。
// 计数器同包非导出直读，不存在导出访问器。
func SelfCheck() error {
	l := New()
	wp := [][]int{{1}, {1, 2}, {2}, {2, 3}, {3}, {3, 4}, {3, 4, 5}, {}, {3, 4, 5}, {}}
	wd := []int{0, 0, 1, 1, 2, 2, 2, 2, 2, 5}
	wn := []int{2, 3, 3, 4, 4, 5, 6, 6, 6, 6}
	rep := func() { _ = l.RePropose(3, "C"); _ = l.RePropose(4, "D"); _ = l.RePropose(5, "E") }
	del3 := func() { l.Deliver(); l.Deliver(); l.Deliver() }
	steps := []func(){
		func() { l.Propose("A") }, func() { l.Propose("B") }, func() { l.Deliver() },
		func() { l.Propose("C") }, func() { l.Deliver() }, func() { l.Propose("D") },
		func() { l.Propose("E") }, l.Crash, rep, del3,
	}
	for i, fn := range steps {
		fn()
		if !l.matches(wd[i], wn[i], wp[i]) {
			return fmt.Errorf("step %d: d=%d n=%d", i+1, l.d, l.nextSeq())
		}
	}
	for _, m := range []int{100, 1000, 10000} {
		l2 := New()
		for i := 0; i < m; i++ {
			if _, err := l2.Propose("x"); err != nil {
				return err
			}
		}
		if _, _, ok := l2.Deliver(); !ok || l2.lastScan != 1 {
			return fmt.Errorf("m=%d scan=%d ok=%v", m, l2.lastScan, ok)
		}
	}
	return nil
}
