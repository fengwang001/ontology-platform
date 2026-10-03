package sparsebitset

import (
	"errors"
	"sync"
	"unsafe"
)

// maxValue 是集合可表示的最大元素个数对应的上界（uint32 全域大小）。
const maxValue = uint64(1) << 32

// 可区分的错误原因，调用方可用 errors.Is 判定。
var (
	// ErrLoGreaterThanHi 表示 AddRange 的 lo 大于 hi。
	ErrLoGreaterThanHi = errors.New("sparsebitset: lo greater than hi")
	// ErrHiExceedsMax 表示 AddRange 的 hi 大于 2^32。
	ErrHiExceedsMax = errors.New("sparsebitset: hi exceeds 2^32")
	// ErrSelectOutOfRange 表示 Select 的 k 不小于集合基数。
	ErrSelectOutOfRange = errors.New("sparsebitset: select index out of range")
)

// Set 是元素为 uint32 的自适应稀疏位集。
// 高 16 位为容器键，低 16 位存于容器内。
// 所有方法可并发调用，效果等价于某个串行顺序。
type Set struct {
	mu         sync.RWMutex
	keys       []uint16 // 与 containers 一一对应，升序
	containers []*container
}

// New 返回一个空集合。
func New() *Set {
	return &Set{}
}

// Add 向集合加入元素 x。
func (s *Set) Add(x uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, v := split(x)
	idx, found := s.findKey(key)
	if !found {
		c := newArrayContainer()
		c.add(v)
		s.insertContainer(idx, key, c)
		return
	}
	c := s.containers[idx]
	if c.add(v) {
		c.normalize()
	}
}

// Remove 从集合删除元素 x；删除不存在的元素为合法空操作。
func (s *Set) Remove(x uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, v := split(x)
	idx, found := s.findKey(key)
	if !found {
		return
	}
	c := s.containers[idx]
	if !c.remove(v) {
		return
	}
	if c.card == 0 {
		s.removeContainer(idx)
		return
	}
	c.normalize()
}

// Contains 报告元素 x 是否在集合中。
func (s *Set) Contains(x uint32) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key, v := split(x)
	idx, found := s.findKey(key)
	if !found {
		return false
	}
	return s.containers[idx].contains(v)
}

// AddRange 加入半开区间 [lo, hi) 内全部值，hi 可取到 2^32。
// lo > hi 时返回 ErrLoGreaterThanHi（优先于 hi 越界检查），
// hi > 2^32 时返回 ErrHiExceedsMax；被拒绝时不改变集合。
// lo == hi 是合法空操作。
func (s *Set) AddRange(lo, hi uint64) error {
	if lo > hi {
		return ErrLoGreaterThanHi
	}
	if hi > maxValue {
		return ErrHiExceedsMax
	}
	if lo == hi {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keyLo := uint32(lo >> 16)
	keyHi := uint32((hi - 1) >> 16)
	for k := keyLo; k <= keyHi; k++ {
		var cLo uint16
		var cHi uint32 = 1 << 16
		if k == keyLo {
			cLo = uint16(lo & 0xffff)
		}
		if k == keyHi {
			cHi = uint32((hi-1)&0xffff) + 1
		}
		s.fillContainerRange(uint16(k), cLo, cHi)
	}
	return nil
}

// And 返回 s 与 o 的交集新集合，每个容器按结果基数重新规范化，
// 交为空的容器不出现。s 与 o 均不被修改。
func (s *Set) And(o *Set) *Set {
	if o == s {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.andLocked(o)
	}
	// 按指针地址确定全局锁顺序，避免双向 And 与写操作交叉时死锁。
	first, second := s, o
	if uintptr(unsafe.Pointer(o)) < uintptr(unsafe.Pointer(s)) {
		first, second = o, s
	}
	first.mu.RLock()
	second.mu.RLock()
	defer first.mu.RUnlock()
	defer second.mu.RUnlock()
	return s.andLocked(o)
}

// Rank 返回集合中不大于 x 的元素个数。
func (s *Set) Rank(x uint32) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key, v := split(x)
	idx, found := s.findKey(key)
	n := 0
	for i := 0; i < idx; i++ {
		n += s.containers[i].card
	}
	if found {
		n += s.containers[idx].rank(v)
	}
	return n
}

// Select 返回集合中第 k 小（从 0 起）的元素。
// k 不小于基数时返回 ErrSelectOutOfRange。
func (s *Set) Select(k int) (uint32, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if k < 0 || k >= s.cardinalityLocked() {
		return 0, ErrSelectOutOfRange
	}
	rem := k
	for i, c := range s.containers {
		if rem < c.card {
			return uint32(s.keys[i])<<16 | uint32(c.selectAt(rem)), nil
		}
		rem -= c.card
	}
	return 0, ErrSelectOutOfRange
}

// Cardinality 返回集合中元素的总数。
func (s *Set) Cardinality() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cardinalityLocked()
}

// Stats 返回数组容器数与位图容器数。
func (s *Set) Stats() (arrays, bitmaps int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.containers {
		if c.isBitmap() {
			bitmaps++
		} else {
			arrays++
		}
	}
	return arrays, bitmaps
}

// split 把元素拆成容器键（高 16 位）与容器内值（低 16 位）。
func split(x uint32) (uint16, uint16) {
	return uint16(x >> 16), uint16(x)
}

// cardinalityLocked 在已持锁的前提下返回总基数。
func (s *Set) cardinalityLocked() int {
	n := 0
	for _, c := range s.containers {
		n += c.card
	}
	return n
}

// findKey 二分查找容器键，返回插入位置与是否命中。
func (s *Set) findKey(key uint16) (int, bool) {
	lo, hi := 0, len(s.keys)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if s.keys[mid] < key {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo, lo < len(s.keys) && s.keys[lo] == key
}

// insertContainer 在 idx 处插入新容器，调用方需保证键不存在。
func (s *Set) insertContainer(idx int, key uint16, c *container) {
	s.keys = append(s.keys, 0)
	copy(s.keys[idx+1:], s.keys[idx:])
	s.keys[idx] = key
	s.containers = append(s.containers, nil)
	copy(s.containers[idx+1:], s.containers[idx:])
	s.containers[idx] = c
}

// removeContainer 删除 idx 处的容器。
func (s *Set) removeContainer(idx int) {
	copy(s.keys[idx:], s.keys[idx+1:])
	s.keys = s.keys[:len(s.keys)-1]
	copy(s.containers[idx:], s.containers[idx+1:])
	s.containers = s.containers[:len(s.containers)-1]
}

// fillContainerRange 在键为 key 的容器中置位 [cLo, cHi)，必要时创建容器。
func (s *Set) fillContainerRange(key uint16, cLo uint16, cHi uint32) {
	idx, found := s.findKey(key)
	if !found {
		c := newArrayContainer()
		c.fillRange(cLo, cHi)
		s.insertContainer(idx, key, c)
		return
	}
	s.containers[idx].fillRange(cLo, cHi)
}

// andLocked 在双方已加读锁的前提下求交集。
func (s *Set) andLocked(o *Set) *Set {
	out := New()
	i, j := 0, 0
	for i < len(s.keys) && j < len(o.keys) {
		switch {
		case s.keys[i] < o.keys[j]:
			i++
		case s.keys[i] > o.keys[j]:
			j++
		default:
			if c := intersect(s.containers[i], o.containers[j]); c != nil {
				out.keys = append(out.keys, s.keys[i])
				out.containers = append(out.containers, c)
			}
			i++
			j++
		}
	}
	return out
}
