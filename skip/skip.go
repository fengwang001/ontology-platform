package skip

import (
	"encoding/binary"
	"errors"
	"math/rand/v2"
	"sync/atomic"

	"ontology/node"
)

const MaxLevel = 24

var (
	ErrDuplicate = errors.New("skip: duplicate key")
	ErrNotFound  = errors.New("skip: key not found")
	ErrBadRange  = errors.New("skip: range requires lo < hi")
)

// KV 是 Range 返回的键值对。
type KV[T any] struct {
	Key int
	Val T
}

// SkipList 是可复现有序跳表；写非并发安全，只读 Find/Range 可并发。
type SkipList[T any] struct {
	head  *node.Node[T]
	rng   *rand.Rand
	level int
	n     int
	cmp   atomic.Uint64 // 非导出：累计 key 比较次数
}

// New 以 seed 构建空表；同 seed、同 key、同插入顺序产出逐字节同构结构。
func New[T any](seed uint64) *SkipList[T] {
	return &SkipList[T]{head: node.New[T](0, *new(T), MaxLevel),
		rng: rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15)), level: 1}
}

func (s *SkipList[T]) Len() int          { return s.n }
func (s *SkipList[T]) CmpCount() uint64  { return s.cmp.Load() }
func (s *SkipList[T]) ResetCmp()         { s.cmp.Store(0) }

// newLevel 消费确定性随机流决定层级（几何分布，p=1/2）。
func (s *SkipList[T]) newLevel() int {
	lvl := 1
	for lvl < MaxLevel && s.rng.Uint64()&1 == 1 {
		lvl++
	}
	return lvl
}

// path 返回每层前驱与第 0 层首个 key >= 参数的节点，并累计 key 比较次数。
func (s *SkipList[T]) path(key int) (*[MaxLevel]*node.Node[T], *node.Node[T]) {
	var up [MaxLevel]*node.Node[T]
	x := s.head
	for i := s.level - 1; i >= 0; i-- {
		for f := x.Next(i); f != nil; f = x.Next(i) {
			s.cmp.Add(1)
			if f.Key >= key {
				break
			}
			x = f
		}
		up[i] = x
	}
	return &up, x.Next(0)
}

// Insert 插入键值对；重复 key 返回 ErrDuplicate，表保持不变。
func (s *SkipList[T]) Insert(key int, val T) error {
	up, next := s.path(key)
	if next != nil && next.Key == key {
		return ErrDuplicate
	}
	lvl, nd := s.newLevel(), node.New(key, val, 0)
	*nd = *node.New(key, val, lvl)
	for i := 0; i < lvl; i++ {
		if i >= s.level {
			s.head.SetNext(i, nd)
			continue
		}
		nd.SetNext(i, up[i].Next(i))
		up[i].SetNext(i, nd)
	}
	if lvl > s.level {
		s.level = lvl
	}
	s.n++
	return nil
}

// Find 命中返回值与 true；未命中返回零值与 false。
func (s *SkipList[T]) Find(key int) (T, bool) {
	if _, next := s.path(key); next != nil && next.Key == key {
		return next.Val, true
	}
	return *new(T), false
}

// Delete 删除 key；不存在返回 ErrNotFound。
func (s *SkipList[T]) Delete(key int) error {
	up, next := s.path(key)
	if next == nil || next.Key != key {
		return ErrNotFound
	}
	for i := 0; i < next.Level(); i++ {
		up[i].SetNext(i, next.Next(i))
	}
	for s.level > 1 && s.head.Next(s.level-1) == nil {
		s.level--
	}
	s.n--
	return nil
}

// Range 返回 [lo, hi) 内按 key 升序的键值对；lo >= hi 返回 ErrBadRange。
func (s *SkipList[T]) Range(lo, hi int) ([]KV[T], error) {
	if lo >= hi {
		return nil, ErrBadRange
	}
	_, x := s.path(lo)
	var out []KV[T]
	for x != nil && x.Key < hi {
		out = append(out, KV[T]{Key: x.Key, Val: x.Val})
		x = x.Next(0)
	}
	return out, nil
}

// MarshalStructure 序列化层级：当前层数 + 第 0 层每节点 (key, level)，不含值。
func (s *SkipList[T]) MarshalStructure() []byte {
	buf := make([]byte, 0, 1+9*s.n)
	buf = append(buf, byte(s.level))
	for x := s.head.Next(0); x != nil; x = x.Next(0) {
		var rec [9]byte
		binary.BigEndian.PutUint64(rec[:8], uint64(x.Key))
		rec[8] = byte(x.Level())
		buf = append(buf, rec[:]...)
}
	return buf
}
