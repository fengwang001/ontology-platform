// Package scan 实现带预算与游标的分批生命周期扫描。
//
// 每个键整键原子处理三个阶段：Expire（追加删除标记）、NoncurrentExpire
// （清理到期的非当前数据版本）、OrphanMarker（清理孤儿删除标记）。预算只
// 决定处理到哪个键为止，因此结果与扫描切分无关且可精确复现。
package scan

import (
	"errors"
	"fmt"
	"sync"

	"ontology/rule"
	"ontology/store"
)

const daySec = int64(86400)

// MaxBudget 是 budget 的上界（含）。
const MaxBudget = int64(1_000_000_000)

var (
	ErrInvalidBudget = errors.New("scan: budget out of range [1, 1e9]")
	// ErrRemoveFailed 标识处理某键时移除失败（整键已回滚）。
	ErrRemoveFailed = store.ErrRemoveFailed
)

// Action 是清单条目的动作类型。
type Action int

const (
	// AddMarker 追加了一个删除标记。
	AddMarker Action = iota
	// Removed 永久删除了一个版本。
	Removed
	// Blocked 版本本应删除但因保留截止大于 now 而保留。
	Blocked
)

func (a Action) String() string {
	switch a {
	case AddMarker:
		return "AddMarker"
	case Removed:
		return "Removed"
	case Blocked:
		return "Blocked"
	}
	return fmt.Sprintf("Action(%d)", int(a))
}

// Entry 是一条结果清单记录。
type Entry struct {
	Key    string
	Ver    uint64
	Action Action
}

func (e Entry) String() string {
	return fmt.Sprintf("(%q v%d %v)", e.Key, e.Ver, e.Action)
}

// Result 是一次 Scan 的结果。
type Result struct {
	// Cursor 是第一个未处理的键；空串表示全部处理完。
	Cursor string
	// Entries 按处理顺序排列：同键内先阶段一再阶段二再阶段三，
	// 同阶段按版本号升序。
	Entries  []Entry
	examined int64
}

// Examined 返回本次已处理键的代价之和（含因移除失败而回滚的键），
// 不超过 budget 加首个键的超出量。
func (r Result) Examined() int64 { return r.examined }

// Scanner 在 store 上按 rule.Set 执行分批扫描。零 Cursor 从头开始。
type Scanner struct {
	st     *store.Store
	rules  *rule.Set
	mu     sync.Mutex
	cursor string
}

func New(st *store.Store, rs *rule.Set) *Scanner {
	return &Scanner{st: st, rules: rs}
}

// dueAt 计算到期时刻：ceil((t+days*86400)/86400)*86400，
// 恰在 UTC 零点则不再进位。
func dueAt(t int64, days int) int64 {
	x := t + int64(days)*daySec
	return (x + daySec - 1) / daySec * daySec
}

// keyPlan 是单个键的处理计划。
type keyPlan struct {
	markers   []int64 // 待追加标记的 c（至多一个）
	removes   []uint64
	dropAdded bool // 阶段三删除本次追加的标记
	hasPhase3 bool
	phase3Ver uint64
	events    []Entry // 阶段二条目，按版本号升序
}

// virtualVer 是计划阶段虚拟标记的占位版本号（大于任何真实版本号）。
const virtualVer = ^uint64(0)

// planKey 基于键的当前版本集计算处理计划，不修改存储。
func planKey(key string, vers []store.Version, rs *rule.Set, now int64) keyPlan {
	var p keyPlan
	if len(vers) == 0 {
		return p
	}
	// 阶段一：Expire。当前版本为数据版本且到期则追加标记，c 取到期时刻。
	if r, ok := rs.Match(key, rule.Expire); ok {
		cur := vers[len(vers)-1]
		if !cur.Marker && now >= dueAt(cur.C, r.Days) {
			p.markers = append(p.markers, dueAt(cur.C, r.Days))
		}
	}
	// 阶段一之后的版本集：追加的标记版本号最大，c 为到期时刻。
	vers2 := append([]store.Version(nil), vers...)
	if len(p.markers) > 0 {
		vers2 = append(vers2, store.Version{Ver: virtualVer, C: p.markers[0], Marker: true})
	}
	// 阶段二：NoncurrentExpire。判定全部基于本阶段开始时的版本集。
	removed := map[uint64]bool{}
	if r, ok := rs.Match(key, rule.NoncurrentExpire); ok {
		n := len(vers2)
		protected := make([]bool, n)
		cnt := 0
		for i := n - 2; i >= 0 && cnt < r.Keep; i-- {
			if !vers2[i].Marker {
				protected[i] = true
				cnt++
			}
		}
		for i := 0; i < n-1; i++ {
			v := vers2[i]
			if v.Marker || protected[i] {
				continue
			}
			became := vers2[i+1].C
			if now < dueAt(became, r.Days) {
				continue
			}
			if v.R > now {
				p.events = append(p.events, Entry{Key: key, Ver: v.Ver, Action: Blocked})
			} else {
				p.events = append(p.events, Entry{Key: key, Ver: v.Ver, Action: Removed})
				p.removes = append(p.removes, v.Ver)
				removed[v.Ver] = true
			}
		}
	}
	// 阶段三：OrphanMarker。只剩一个版本且是标记则永久删除。
	if _, ok := rs.Match(key, rule.OrphanMarker); ok {
		var remain []store.Version
		for _, v := range vers2 {
			if !removed[v.Ver] {
				remain = append(remain, v)
			}
		}
		if len(remain) == 1 && remain[0].Marker {
			p.hasPhase3 = true
			if remain[0].Ver == virtualVer {
				p.dropAdded = true
			} else {
				p.phase3Ver = remain[0].Ver
			}
		}
	}
	return p
}

// Scan 从游标起按键升序分批处理，budget 取值 [1, 1e9]。
// 每个键的代价为处理前版本记录数；仅当已用代价加本键代价不大于
// budget 时处理本键，但每次调用的第一个键无论如何都处理。
// 某键移除失败时整键回滚，返回 ErrRemoveFailed，游标指向该键。
func (s *Scanner) Scan(now int64, budget int64) (Result, error) {
	if budget < 1 || budget > MaxBudget {
		return Result{}, ErrInvalidBudget
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var res Result
	first := true
	for _, key := range s.st.Keys() {
		if key < s.cursor {
			continue
		}
		vers := s.st.Versions(key)
		cost := int64(len(vers))
		if !first && res.examined+cost > budget {
			res.Cursor = key
			s.cursor = key
			return res, nil
		}
		first = false
		res.examined += cost
		p := planKey(key, vers, s.rules, now)
		removes := p.removes
		if p.hasPhase3 && !p.dropAdded {
			removes = append(removes, p.phase3Ver)
		}
		added, err := s.st.Apply(key, p.markers, removes, p.dropAdded)
		if err != nil {
			res.Cursor = key
			s.cursor = key
			return res, fmt.Errorf("scan: key %q: %w", key, err)
		}
		if len(added) > 0 {
			res.Entries = append(res.Entries, Entry{Key: key, Ver: added[0].Ver, Action: AddMarker})
		}
		res.Entries = append(res.Entries, p.events...)
		if p.hasPhase3 {
			ver := p.phase3Ver
			if p.dropAdded {
				ver = added[0].Ver
			}
			res.Entries = append(res.Entries, Entry{Key: key, Ver: ver, Action: Removed})
		}
	}
	s.cursor = ""
	return res, nil
}
