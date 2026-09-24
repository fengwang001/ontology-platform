// Package api 是对外门面：New/Publish/Acquire/Get/Release/AliveCount/SelfCheck。
package api

import (
	"errors"
	"fmt"

	"ontology/store"
)

// 可判定哨兵错误（与 store 同一实例，四者互不相同）。
var (
	ErrEmptyStore    = store.ErrEmptyStore
	ErrNegativeValue = store.ErrNegativeValue
	ErrDoubleRelease = store.ErrDoubleRelease
	ErrUseAfterFree  = store.ErrUseAfterFree
)

// DB 是物化状态存储的对外句柄。
type DB struct{ s *store.Store }

func New() *DB { return &DB{s: store.New()} }

func (d *DB) Publish(v int) error { return d.s.Publish(v) }

func (d *DB) AliveCount() int { return d.s.AliveCount() }

// Acquire 返回指向当前版本的句柄。
func (d *DB) Acquire() (*Handle, error) {
	h, err := d.s.Acquire()
	if err != nil {
		return nil, err
	}
	return &Handle{h: h}, nil
}

// Handle 是指向某版本的一份引用的对外句柄。
type Handle struct{ h *store.Handle }

func (h *Handle) Get() (int, error) { return h.h.Get() }

func (h *Handle) Release() error { return h.h.Release() }

// Refs 返回句柄所指版本当前的引用计数（只读检查用）。
func (h *Handle) Refs() int { return h.h.Refs() }

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (d *DB) SelfCheck() error {
	// 不变量 3+2：零引用立即回收、共享不误回收。
	if err := d.Publish(10); err != nil {
		return err
	}
	h1, err := d.Acquire()
	if err != nil {
		return err
	}
	if err := d.Publish(20); err != nil {
		return err
	}
	if got := d.AliveCount(); got != 2 {
		return fmt.Errorf("selfcheck: alive=%d want 2", got)
	}
	if v, err := h1.Get(); err != nil || v != 10 {
		return fmt.Errorf("selfcheck: shared get=%v,%v want 10,nil", v, err)
	}
	if err := h1.Release(); err != nil {
		return err
	}
	if got := d.AliveCount(); got != 1 {
		return fmt.Errorf("selfcheck: after release alive=%d want 1", got)
	}
	// 不变量 4：失败不留痕。
	before := d.AliveCount()
	for _, want := range []error{ErrDoubleRelease, ErrNegativeValue} {
		var err error
		switch want {
		case ErrDoubleRelease:
			err = h1.Release()
		case ErrNegativeValue:
			err = d.Publish(-1)
		}
		if !errors.Is(err, want) {
			return fmt.Errorf("selfcheck: want %v got %v", want, err)
		}
	}
	if d.AliveCount() != before {
		return errors.New("selfcheck: rejected op changed state")
	}
	// use-after-free：h1 已释放且其版本已回收。
	if _, err := h1.Get(); !errors.Is(err, ErrUseAfterFree) {
		return fmt.Errorf("selfcheck: want use-after-free got %v", err)
	}
	// 空 store 的 Acquire。
	if _, err := New().Acquire(); !errors.Is(err, ErrEmptyStore) {
		return fmt.Errorf("selfcheck: want empty-store got %v", err)
	}
	// 回收 O(1)。
	if !store.ReleaseCheckedO1() {
		return errors.New("selfcheck: release not O(1)")
	}
	return nil
}
