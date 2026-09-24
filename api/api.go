// Package api 是引用计数物化状态存储的对外入口，依赖 store，反向依赖不存在。
package api

import "ontology/store"

// 四类可判定哨兵错误，彼此互不相同；用 errors.Is 判定。
var (
	ErrDoubleRelease = store.ErrDoubleRelease
	ErrUseAfterFree  = store.ErrUseAfterFree
	ErrEmptyStore    = store.ErrEmptyStore
	ErrNegativeValue = store.ErrNegativeValue
)

// Store 是对外存储句柄。
type Store struct{ s *store.Store }

// Handle 是对外版本句柄。
type Handle struct{ h *store.Handle }

// New 创建空存储。
func New() *Store { return &Store{s: store.New()} }

// Publish 发布一个 value≥0 的新版本；负值返回 ErrNegativeValue 且状态不变。
func (s *Store) Publish(v int) error { return s.s.Publish(v) }

// Acquire 获取指向当前版本的句柄；空存储返回 ErrEmptyStore。
func (s *Store) Acquire() (*Handle, error) {
	h, err := s.s.Acquire()
	if err != nil {
		return nil, err
	}
	return &Handle{h: h}, nil
}

// Get 返回句柄所指版本的发布值；句柄已释放或版本已回收返回 ErrUseAfterFree。
func (h *Handle) Get() (int, error) { return h.h.Get() }

// Release 释放一份引用，refs 归零立即回收；双重释放返回 ErrDoubleRelease。
func (h *Handle) Release() error { return h.h.Release() }

// AliveCount 返回当前存活（未回收）的版本个数。
func (s *Store) AliveCount() int { return s.s.AliveCount() }

// SelfCheck 在内部存储上重放内置操作序列，逐条核验四条不变量与 O(1) 回收检查；
// 任一不成立即返回可判定错误。非导出的回收检查计数器只在此过程内部被读取。
func (s *Store) SelfCheck() error { return s.s.SelfCheck() }
