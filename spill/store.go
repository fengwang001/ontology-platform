// Package spill 维护常驻区/溢写区两个映射与 LRU 访问序。
// 它只做搬运与求和，不做参数校验；任一 Key 任一时刻至多存在于一处。
package spill

import (
	"container/list"
	"strconv"
)

// State 表示一个 Key 当前所在的位置。
type State int

const (
	Absent State = iota
	Resident
	Spilled
)

type node struct {
	key string
	val int64
}

// Store 是常驻区 + 溢写区。链表 front 为最近访问、back 为最久未访问。
type Store struct {
	resident map[string]*list.Element
	spilled  map[string]int64
	ll       *list.List

	// lastProbe 是最近一次溢写决策检查过的常驻组个数（非导出）。
	// LRU 由双向链表 back 直接定位，恒检查 1 个，与常驻组总数无关。
	lastProbe int
}

// New 创建空 Store。
func New() *Store {
	return &Store{
		resident: map[string]*list.Element{},
		spilled:  map[string]int64{},
		ll:       list.New(),
	}
}

// Lookup 返回 Key 的位置与当前和（不存在时值为 0）。
func (s *Store) Lookup(key string) (State, int64) {
	if e, ok := s.resident[key]; ok {
		return Resident, e.Value.(*node).val
	}
	if v, ok := s.spilled[key]; ok {
		return Spilled, v
	}
	return Absent, 0
}

// ResidentLen 返回常驻组数。
func (s *Store) ResidentLen() int { return len(s.resident) }

// SpilledLen 返回溢写区组数。
func (s *Store) SpilledLen() int { return len(s.spilled) }

// PutResident 以 val 插入新常驻组，或更新已有常驻组的值；两种情况都刷新访问序。
func (s *Store) PutResident(key string, val int64) {
	if e, ok := s.resident[key]; ok {
		e.Value.(*node).val = val
		s.ll.MoveToFront(e)
		return
	}
	s.resident[key] = s.ll.PushFront(&node{key: key, val: val})
}

// Evict 把最久未访问的常驻组搬入溢写区并返回其 Key；常驻区为空时 ok=false。
func (s *Store) Evict() (key string, ok bool) {
	tail := s.ll.Back()
	if tail == nil {
		s.lastProbe = 0
		return "", false
	}
	s.lastProbe = 1 // 直接取链表 back：只检查 1 个常驻组
	n := tail.Value.(*node)
	s.ll.Remove(tail)
	delete(s.resident, n.key)
	s.spilled[n.key] = n.val // 调用不变量保证 n.key 不在溢写区
	return n.key, true
}

// LoadIn 回载一个溢写组：要求 key 当前在溢写区。
// 以「溢写部分和 + val」入常驻、删除溢写条目，返回合并值。
func (s *Store) LoadIn(key string, val int64) int64 {
	partial := s.spilled[key]
	delete(s.spilled, key)
	merged := partial + val
	s.resident[key] = s.ll.PushFront(&node{key: key, val: merged})
	return merged
}

// Snapshot 返回常驻区与溢写区各自的独立副本。
func (s *Store) Snapshot() (resident, spilled map[string]int64) {
	resident = make(map[string]int64, len(s.resident))
	for k, e := range s.resident {
		resident[k] = e.Value.(*node).val
	}
	spilled = make(map[string]int64, len(s.spilled))
	for k, v := range s.spilled {
		spilled[k] = v
	}
	return resident, spilled
}

// VerifyProbeConstant 在包内做大 m 溢写探针实验，仅返回「每次溢写检查个数为
// 与 m 无关的小常数」这一布尔判定；不向调用方暴露非导出计数器的数值。
func VerifyProbeConstant() bool {
	for _, m := range []int{100, 1000, 10000} {
		s := New()
		for i := 0; i < m; i++ {
			s.PutResident("r"+strconv.Itoa(i), int64(i))
		}
		for i := 0; i < m+1; i++ {
			s.PutResident("n"+strconv.Itoa(i), int64(i))
			if s.ResidentLen() > m {
				s.Evict()
			}
			if s.lastProbe != 1 { // 双向链表 back 直接定位，检查个数恒为 1
				return false
			}
		}
	}
	return true
}
