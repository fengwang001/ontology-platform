// Package lsn 维护一个有序且唯一的 LSN 记录集合：
// 插入、升序二分定位、空洞计算。不依赖其他包。
package lsn

import (
	"errors"
	"sync/atomic"
)

// 可判定哨兵错误，四者互不相同。
var (
	ErrNegative   = errors.New("lsn: negative LSN")
	ErrDuplicate  = errors.New("lsn: duplicate LSN")
	ErrUnknown    = errors.New("lsn: unknown LSN")
	ErrOutOfRange = errors.New("lsn: new number out of range")
)

// Set 是升序、唯一的 LSN 集合。vals 非并发安全，由调用方加锁；
// lastCmps 走原子写，读路径（FindNew/FindOld）并发调用安全。
type Set struct {
	vals     []int64 // 升序、唯一
	lastCmps int64   // 最近一次 FindNew/FindOld 定位比较过的记录个数（非导出，原子写）
}

// New 返回空集合。
func New() *Set { return &Set{} }

// locate 二分定位，返回应处下标、是否命中、比较过的记录个数。
// 只读 vals，不写任何状态；计数器由调用方在操作成功时提交。
func (s *Set) locate(v int64) (idx int, found bool, cmps int) {
	lo, hi := 0, len(s.vals)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		cmps++ // 每轮只与 vals[mid] 这一条记录比较
		if s.vals[mid] == v {
			return mid, true, cmps
		}
		if s.vals[mid] < v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo, false, cmps
}

// Add 插入一条 LSN。负号或重复时整体失败，集合与计数器均不变。
func (s *Set) Add(v int64) error {
	if v < 0 {
		return ErrNegative
	}
	i, found, _ := s.locate(v)
	if found {
		return ErrDuplicate
	}
	s.vals = append(s.vals, 0)
	copy(s.vals[i+1:], s.vals[i:])
	s.vals[i] = v
	return nil
}

// IndexOf 返回 v 的新号（升序位次，0 起）。未知 LSN 报错且计数器不变。
func (s *Set) IndexOf(v int64) (int, error) {
	i, found, cmps := s.locate(v)
	if !found {
		return 0, ErrUnknown
	}
	atomic.StoreInt64(&s.lastCmps, int64(cmps))
	return i, nil
}

// ValueAt 返回新号 i 对应的旧 LSN。越界报错且计数器不变；
// 成功时为按下标直接寻址，比较 0 条记录。
func (s *Set) ValueAt(i int) (int64, error) {
	if i < 0 || i >= len(s.vals) {
		return 0, ErrOutOfRange
	}
	atomic.StoreInt64(&s.lastCmps, 0)
	return s.vals[i], nil
}

// Len 返回记录数 n。
func (s *Set) Len() int { return len(s.vals) }

// Holes 返回 [min,max] 内缺失的整数，升序；n<2 或无缺失时为空。
func (s *Set) Holes() []int64 {
	if len(s.vals) < 2 {
		return nil
	}
	lo, hi := s.vals[0], s.vals[len(s.vals)-1]
	var out []int64
	for i, want := 0, lo; ; want++ { // want==hi 时先处理后退出，避免 MaxInt64 自增溢出
		if i < len(s.vals) && s.vals[i] == want {
			i++
		} else {
			out = append(out, want)
		}
		if want == hi {
			return out
		}
	}
}
