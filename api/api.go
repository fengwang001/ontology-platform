// Package api 对外提供写者优先读写自旋锁：读/写/读升写、Snapshot 与 SelfCheck。
package api

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"ontology/lock"
	"ontology/state"
)

var ( // 可判定哨兵错误（定义于 state，此处再导出），互不相同。
	ErrNotReader, ErrNotWriter              = state.ErrNotReader, state.ErrNotWriter
	ErrReaderReentry, ErrWriterReentry      = state.ErrReaderReentry, state.ErrWriterReentry
	ErrUpgradeNotReader, ErrUpgradeConflict = state.ErrUpgradeNotReader, state.ErrUpgradeConflict
)

type Snapshot = state.Snapshot

type RWLock struct{ l *lock.Lock }

func New() *RWLock                         { return &RWLock{l: lock.New(state.New())} }
func (r *RWLock) AcquireRead(o int) error  { return r.l.AcquireRead(o) }
func (r *RWLock) ReleaseRead(o int) error  { return r.l.ReleaseRead(o) }
func (r *RWLock) AcquireWrite(o int) error { return r.l.AcquireWrite(o) }
func (r *RWLock) ReleaseWrite(o int) error { return r.l.ReleaseWrite(o) }
func (r *RWLock) TryUpgrade(o int) error   { return r.l.TryUpgrade(o) }
func (r *RWLock) Snapshot() Snapshot       { return r.l.Snapshot() }

// naive 是 sync.Mutex 保护的朴素参照，与真锁同一套规则，供不变量 1 逐步比对。
type naive struct {
	mu      sync.Mutex
	readers map[int]bool
	writer  int
}

func newNaive() *naive { return &naive{readers: map[int]bool{}, writer: -1} }

func (n *naive) apply(op string, o int) (blocked bool, err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	isR, isW, nR := n.readers[o], n.writer >= 0, len(n.readers)
	fail := func(e error) (bool, error) { return false, e }
	switch {
	case op == "AR" && isR:
		return fail(state.ErrReaderReentry)
	case op == "AR" && isW:
		return true, nil
	case op == "AR":
		n.readers[o] = true
	case op == "RR" && !isR:
		return fail(state.ErrNotReader)
	case op == "RR":
		delete(n.readers, o)
	case op == "AW" && n.writer == o:
		return fail(state.ErrWriterReentry)
	case op == "AW" && (isW || nR > 0):
		return true, nil
	case op == "AW":
		n.writer = o
	case op == "RW" && n.writer != o:
		return fail(state.ErrNotWriter)
	case op == "RW":
		n.writer = -1
	case op == "UP" && !isR:
		return fail(state.ErrUpgradeNotReader)
	case op == "UP" && nR != 1:
		return fail(state.ErrUpgradeConflict)
	case op == "UP":
		delete(n.readers, o)
		n.writer = o
	}
	return false, nil
}

func (n *naive) snapshot() Snapshot {
	n.mu.Lock()
	defer n.mu.Unlock()
	s := Snapshot{Writer: n.writer}
	for o := range n.readers {
		s.Readers = append(s.Readers, o)
	}
	sort.Ints(s.Readers)
	return s
}

var realOps = map[string]func(*RWLock, int) error{
	"AR": (*RWLock).AcquireRead, "RR": (*RWLock).ReleaseRead,
	"AW": (*RWLock).AcquireWrite, "RW": (*RWLock).ReleaseWrite, "UP": (*RWLock).TryUpgrade,
}

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	rw, nv := New(), newNaive()
	for i, f := range strings.Fields("AR1 AR2 AR1 UP1 UP9 RR9 RW1 AR3 RR1 UP2 RR2 RR3 AW4 AW4 RW5 RW4 AR5 UP5 RW5 RR5") {
		op, o := f[:2], int(f[2]-'0')
		blocked, want := nv.apply(op, o)
		before, got := rw.Snapshot(), realOps[op](rw, o)
		now := rw.Snapshot()
		switch {
		case blocked:
			return fmt.Errorf("步骤 %d 意外阻塞", i)
		case !errors.Is(got, want):
			return fmt.Errorf("步骤 %d 错误 %v != %v", i, got, want)
		case !reflect.DeepEqual(now, nv.snapshot()):
			return fmt.Errorf("步骤 %d 与朴素参照分叉", i)
		case got != nil && !reflect.DeepEqual(before, now):
			return fmt.Errorf("步骤 %d 失败留痕", i)
		case now.Writer >= 0 && len(now.Readers) > 0:
			return errors.New("互斥性被破坏")
		}
	}
	if err := checkPreference(); err != nil || !state.CheckProbeBound() {
		return fmt.Errorf("自检失败: %v", err)
	}
	return nil
}

// checkPreference 核验写者优先：等待写者存在时新读者不得先获读。
func checkPreference() error {
	rw := New()
	_ = rw.AcquireRead(1)
	_ = rw.AcquireRead(2)
	t3, t4 := make(chan error, 1), make(chan error, 1)
	go func() { t3 <- rw.AcquireWrite(3) }()
	ok := spin(func() bool { return rw.Snapshot().WaitingWriters == 1 })
	go func() { t4 <- rw.AcquireRead(4) }()
	ok = ok && spin(func() bool { return len(rw.Snapshot().WaitingReaders) == 1 })
	ok = ok && len(rw.Snapshot().Readers) == 2 // T4 未先于 T3 获读
	_ = rw.ReleaseRead(1)
	_ = rw.ReleaseRead(2)
	ok = ok && <-t3 == nil && rw.Snapshot().Writer == 3 // 写者优先获写
	_ = rw.ReleaseWrite(3)
	ok = ok && <-t4 == nil && len(rw.Snapshot().Readers) == 1 // T4 随后获读
	if err := rw.ReleaseRead(4); !ok || err != nil {
		return errors.New("写者优先不成立")
	}
	return nil
}

func spin(cond func() bool) bool {
	for i := 0; i < 1<<28 && !cond(); i++ {
	}
	return cond()
}
