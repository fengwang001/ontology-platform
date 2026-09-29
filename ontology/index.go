package ontology

import (
	"errors"
	"sort"
	"sync"
)

// 可区分的拒绝原因。
var (
	ErrEmptyKey      = errors.New("ontology: primary key must not be empty")
	ErrDeleteMissing = errors.New("ontology: cannot delete a primary key that does not exist")
	ErrUnknownOp     = errors.New("ontology: unknown operation kind in batch")
)

// OpKind 标识一批原子写入中的操作类型。
type OpKind int

const (
	OpUpsert OpKind = iota + 1
	OpDelete
)

// Op 是一批写入中的单条操作。
type Op struct {
	Kind OpKind
	Key  string
	Val  int // 仅 Kind == OpUpsert 时使用
}

// Index 是单字段（整数）二级索引的增量维护器。
//
// records 保存主键 -> 当前字段值；index 保存字段值 -> 该值下按主键
// 升序排列的主键列表。读写通过 rw 保护。
type Index struct {
	rw      sync.RWMutex
	records map[string]int
	index   map[int][]string
}

// New 创建一个空的索引维护器。
func New() *Index {
	return &Index{
		records: make(map[string]int),
		index:   make(map[int][]string),
	}
}

// Apply 原子地应用一批写入：任一条非法则整批拒绝。
//
// 校验规则（命中任一即整批拒绝、状态不变）：
//   - 主键为空：返回 ErrEmptyKey；
//   - 删除当前（应用批内此前操作后）不存在的主键：返回 ErrDeleteMissing；
//   - 未知操作类型：返回 ErrUnknownOp。
//
// 校验与应用在同一把写锁内完成，因此“先删旧值索引组、再插新值索引组”
// 两步对外表现为一次原子更新；新值等于旧值时不产生任何索引改动。
// 同一批次内对同一主键的多条 upsert 以后者为准。
func (ix *Index) Apply(ops []Op) error {
	ix.rw.Lock()
	defer ix.rw.Unlock()

	// 1) 预演校验：计算批次结束后每个主键的效果，不落任何变更。
	type effect struct {
		delete bool
		val    int
	}
	proposed := make(map[string]effect, len(ops))
	for _, op := range ops {
		switch op.Kind {
		case OpUpsert:
			if op.Key == "" {
				return ErrEmptyKey
			}
			proposed[op.Key] = effect{val: op.Val}
		case OpDelete:
			if op.Key == "" {
				return ErrEmptyKey
			}
			if _, inBatch := proposed[op.Key]; inBatch {
				delete(proposed, op.Key)
				continue
			}
			if _, exists := ix.records[op.Key]; !exists {
				return ErrDeleteMissing
			}
			proposed[op.Key] = effect{delete: true}
		default:
			return ErrUnknownOp
		}
	}

	// 2) 全部合法后才应用变更。
	for key, eff := range proposed {
		oldVal, existed := ix.records[key]
		switch {
		case eff.delete:
			if existed {
				ix.removeIndex(oldVal, key)
				delete(ix.records, key)
			}
		case !existed:
			ix.records[key] = eff.val
			ix.insertIndex(eff.val, key)
		case oldVal != eff.val:
			// 先从旧值索引组删除，再插入新值索引组。
			ix.removeIndex(oldVal, key)
			ix.insertIndex(eff.val, key)
			ix.records[key] = eff.val
		}
		// oldVal == eff.val：无操作，不产生重复索引项。
	}
	return nil
}

// Put 插入或更新单条记录。
func (ix *Index) Put(key string, val int) error {
	return ix.Apply([]Op{{Kind: OpUpsert, Key: key, Val: val}})
}

// Remove 删除一条记录；主键不存在返回 ErrDeleteMissing。
func (ix *Index) Remove(key string) error {
	return ix.Apply([]Op{{Kind: OpDelete, Key: key}})
}

// Equal 返回字段值等于 val 的主键列表（主键升序）。
func (ix *Index) Equal(val int) []string {
	ix.rw.RLock()
	defer ix.rw.RUnlock()
	return cloneStrings(ix.index[val])
}

// Range 返回字段值落在 [lo, hi) 的主键列表（字段值升序、同值主键升序）。
func (ix *Index) Range(lo, hi int) []string {
	ix.rw.RLock()
	defer ix.rw.RUnlock()

	if lo >= hi {
		return []string{}
	}
	values := make([]int, 0, len(ix.index))
	for val := range ix.index {
		if val >= lo && val < hi {
			values = append(values, val)
		}
	}
	sort.Ints(values)

	var out []string
	for _, val := range values {
		out = append(out, ix.index[val]...)
	}
	return out
}

// Snapshot 返回记录与索引的深拷贝快照，用于整体重扫核对。
func (ix *Index) Snapshot() (map[string]int, map[int][]string) {
	ix.rw.RLock()
	defer ix.rw.RUnlock()

	records := make(map[string]int, len(ix.records))
	for k, v := range ix.records {
		records[k] = v
	}
	index := make(map[int][]string, len(ix.index))
	for v, keys := range ix.index {
		index[v] = cloneStrings(keys)
	}
	return records, index
}

// insertIndex 把主键插入字段值对应的索引组并保持主键升序。
func (ix *Index) insertIndex(val int, key string) {
	group := ix.index[val]
	pos := sort.SearchStrings(group, key)
	if pos < len(group) && group[pos] == key {
		return // 同组内已存在，幂等保护，绝不产生重复项。
	}
	group = append(group, "")
	copy(group[pos+1:], group[pos:])
	group[pos] = key
	ix.index[val] = group
}

// removeIndex 从字段值对应的索引组移除主键；组变空时删除该字段值桶。
func (ix *Index) removeIndex(val int, key string) {
	group := ix.index[val]
	pos := sort.SearchStrings(group, key)
	if pos == len(group) || group[pos] != key {
		return
	}
	group = append(group[:pos], group[pos+1:]...)
	if len(group) == 0 {
		delete(ix.index, val)
		return
	}
	ix.index[val] = group
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
