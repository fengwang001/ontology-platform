// Package store 在进程内存中维护存活行、按版本有序的墓碑集合与全局水位 G，
// 逐条处理事件并在每条之后清除到期墓碑。依赖 rule，不依赖 api。
package store

import (
	"errors"
	"maps"
	"slices"
	"sort"

	"ontology/rule"
)

// 可判定的哨兵错误。
var (
	ErrBadEvent = errors.New("store: invalid event")
	ErrTooMany  = errors.New("store: live rows + tombstones exceed maxKeys")
)

// Event 是一条 CDC 变更：Op 取 'U'（upsert）或 'D'（删除）。
type Event struct {
	Op  byte
	Key string
	Ver int64
	Val string
}

// Row 是存活行：值与版本。
type Row struct {
	Val string
	Ver int64
}

type tombEnt struct {
	ver int64
	key string
}

// Store 不是并发安全的，调用方（api 包）负责加锁。
type Store struct {
	r       int64
	maxKeys int
	rows    map[string]Row
	tombs   map[string]int64
	sorted  []tombEnt // 按 ver 升序；含惰性丢弃的失效条目
	G       int64
	Ignored int64
	// checked 记录最近一次事件处理的清除阶段检查过的墓碑条目个数。
	// 非导出，不出现在任何公开接口。
	checked int
}

func New(r int64, maxKeys int) *Store {
	return &Store{r: r, maxKeys: maxKeys, rows: map[string]Row{}, tombs: map[string]int64{}}
}

// Clone 深拷贝，供 api 实现整批原子生效。
func (s *Store) Clone() *Store {
	c := *s
	c.rows = maps.Clone(s.rows)
	c.tombs = maps.Clone(s.tombs)
	c.sorted = slices.Clone(s.sorted)
	return &c
}

func (s *Store) cur(key string) int64 {
	rv, hasRow := s.rows[key]
	tv, hasTomb := s.tombs[key]
	return rule.Current(rv.Ver, tv, hasRow, hasTomb)
}

// ApplyOne 按固定顺序处理一条事件：①求 cur ②应用或忽略 ③更新 G ④清除到期墓碑 ⑤容量检查。
// ⑤失败时本对象可能已部分变更，调用方须在副本上执行（见 api.Apply）。
func (s *Store) ApplyOne(e Event) error {
	if e.Key == "" || e.Ver <= 0 || (e.Op != 'U' && e.Op != 'D') {
		return ErrBadEvent
	}
	if rule.ShouldApply(e.Ver, s.cur(e.Key)) {
		if e.Op == 'U' {
			s.rows[e.Key] = Row{Val: e.Val, Ver: e.Ver}
			delete(s.tombs, e.Key) // sorted 中留下失效条目，清除阶段惰性丢弃
		} else {
			delete(s.rows, e.Key)
			s.tombs[e.Key] = e.Ver
			i := sort.Search(len(s.sorted), func(i int) bool { return s.sorted[i].ver >= e.Ver })
			s.sorted = append(s.sorted, tombEnt{})
			copy(s.sorted[i+1:], s.sorted[i:])
			s.sorted[i] = tombEnt{ver: e.Ver, key: e.Key}
		}
	} else {
		s.Ignored++
	}
	if e.Ver > s.G {
		s.G = e.Ver
	}
	s.purge()
	if len(s.rows)+len(s.tombs) > s.maxKeys {
		return ErrTooMany
	}
	return nil
}

// purge 从有序切片头部定位到期者：检查个数 = 本次清除数 + 本次丢弃失效条目数 + 至多 1。
func (s *Store) purge() {
	s.checked = 0
	n := 0
	for n < len(s.sorted) {
		s.checked++
		if !rule.Expired(s.G, s.sorted[n].ver, s.r) {
			break
		}
		n++
	}
	for _, e := range s.sorted[:n] {
		if v, ok := s.tombs[e.key]; ok && v == e.ver {
			delete(s.tombs, e.key)
		}
	}
	s.sorted = s.sorted[n:]
}

func (s *Store) Get(key string) (Row, bool) {
	r, ok := s.rows[key]
	return r, ok
}

func (s *Store) Tomb(key string) (int64, bool) {
	v, ok := s.tombs[key]
	return v, ok
}
