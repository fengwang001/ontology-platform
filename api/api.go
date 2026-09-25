// Package api 是无锁有序链表的对外门面：关闭标志、哨兵错误与自检。
package api

import (
	"errors"
	"fmt"
	"sync/atomic"

	"ontology/hlist"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrDuplicate = hlist.ErrDuplicate
	ErrNotFound  = hlist.ErrNotFound
	ErrClosed    = errors.New("api: list closed")
)

// List 是对外链表句柄。
type List struct {
	l      *hlist.List
	closed atomic.Bool
}

// New 返回空链表。
func New() *List { return &List{l: hlist.New()} }

// Insert 插入 k；重复报 ErrDuplicate，已关闭报 ErrClosed，均不改状态。
func (a *List) Insert(k int) error {
	if a.closed.Load() {
		return ErrClosed
	}
	return a.l.Insert(k)
}

// Delete 删除 k；不存在报 ErrNotFound，已关闭报 ErrClosed，均不改状态。
func (a *List) Delete(k int) error {
	if a.closed.Load() {
		return ErrClosed
	}
	return a.l.Delete(k)
}

// Contains 报告 k 是否存在且未删除；关闭后仍可读。
func (a *List) Contains(k int) bool { return a.l.Contains(k) }

// Close 置关闭标志（终态）；之后 Insert/Delete 持续报 ErrClosed。
func (a *List) Close() error {
	a.closed.Store(true)
	return nil
}

// List 返回当前未删除 key 的升序切片。
func (a *List) List() []int { return a.l.List() }

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (a *List) SelfCheck() error {
	l := New()
	ref := map[int]bool{}
	// 不变量 1+2：固定伪随机序列与朴素参照逐次对拍 Contains 与 List。
	seed := uint32(1)
	rnd := func(n int) int { seed = seed*1664525 + 1013904223; return int(seed>>8) % n }
	for i := 0; i < 2000; i++ {
		k := rnd(64)
		switch rnd(3) {
		case 0:
			if err := l.Insert(k); (err == nil) == ref[k] {
				return fmt.Errorf("insert(%d) 与参照不一致", k)
			}
			ref[k] = true
		case 1:
			if err := l.Delete(k); (err == nil) != ref[k] {
				return fmt.Errorf("delete(%d) 与参照不一致", k)
			}
			ref[k] = false
		default:
			if l.Contains(k) != ref[k] {
				return fmt.Errorf("contains(%d) 与参照不一致", k)
			}
		}
	}
	prev := -1 << 60
	for _, k := range l.List() { // 不变量 2：严格升序即无重复
		if k <= prev || !ref[k] {
			return fmt.Errorf("List 非严格升序或与参照不符: %d", k)
		}
		prev = k
	}
	// 不变量 3：删除后立即不可见，且不影响其他 key。
	l2 := New()
	_ = l2.Insert(1)
	_ = l2.Insert(2)
	_ = l2.Insert(3)
	if err := l2.Delete(2); err != nil || l2.Contains(2) || !l2.Contains(1) || !l2.Contains(3) {
		return errors.New("删除可见性不成立")
	}
	// 不变量 4：被拒操作零变化；已关闭为终态。
	before := fmt.Sprint(l2.List())
	if err := l2.Insert(1); !errors.Is(err, ErrDuplicate) {
		return errors.New("重复插入未报 ErrDuplicate")
	}
	if err := l2.Delete(99); !errors.Is(err, ErrNotFound) {
		return errors.New("删除不存在未报 ErrNotFound")
	}
	if fmt.Sprint(l2.List()) != before {
		return errors.New("被拒操作改变了链表")
	}
	_ = l2.Close()
	if err := l2.Insert(9); !errors.Is(err, ErrClosed) {
		return errors.New("关闭后插入未报 ErrClosed")
	}
	if err := l2.Delete(1); !errors.Is(err, ErrClosed) {
		return errors.New("关闭后删除未报 ErrClosed")
	}
	if !l2.Contains(1) || fmt.Sprint(l2.List()) != before {
		return errors.New("关闭后状态被改变")
	}
	return nil
}
