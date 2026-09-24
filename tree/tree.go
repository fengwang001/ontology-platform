// Package tree 实现 memtable、flush、层与合并调度、Get 可见性。依赖 seg。
package tree

import (
	"errors"
	"maps"
	"slices"
	"sync"

	"ontology/seg"
)

var (
	ErrEmptyKey = errors.New("tree: empty key")         // key 为空串的被拒操作
	ErrParam    = errors.New("tree: invalid parameter") // 构造参数非法
)

// Tree 是分层合并的写入路径：memtable + 不可变段 + size-tiered 分层合并。
type Tree struct {
	mu      sync.RWMutex
	cap     int
	fanout  int
	maxLvl  int
	mem     []seg.Record
	levels  [][]seg.Segment
	nextID  int
	scanned int // 最近一次 flush 后决定合并候选时扫描的段数（非导出）
}

// New 校验参数并构造空 Tree。
func New(cap, fanout, maxLevel int) (*Tree, error) {
	if cap <= 0 || fanout < 2 || maxLevel < 1 {
		return nil, ErrParam
	}
	return &Tree{cap: cap, fanout: fanout, maxLvl: maxLevel, levels: make([][]seg.Segment, maxLevel+1), nextID: 1}, nil
}

// Put 追加 Put 记录，Del 追加墓碑；key 为空串拒绝且不改状态。
func (t *Tree) Put(key, val string) error { return t.add(seg.Record{Key: key, Val: val}) }
func (t *Tree) Del(key string) error      { return t.add(seg.Record{Key: key, Del: true}) }
func (t *Tree) add(r seg.Record) error {
	if r.Key == "" {
		return ErrEmptyKey
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.mem) >= t.cap { // 写入前已满 cap 条：先冻结成段
		t.flushLocked()
	}
	t.mem = append(t.mem, r)
	return nil
}

// flushLocked 把 memtable 冻结为层 0 段，随后逐层检查 fanout 并级联合并。
func (t *Tree) flushLocked() {
	t.levels[0] = append(t.levels[0], seg.Segment{ID: t.nextID, Level: 0, Recs: t.mem})
	t.nextID++
	t.mem = nil
	t.scanned = 0
	for lvl := 0; lvl <= t.maxLvl && len(t.levels[lvl]) >= t.fanout; lvl++ {
		t.scanned += t.fanout // 候选即最旧 fanout 个，O(1) 判定，不逐段扫描
		olds := t.levels[lvl][:t.fanout]
		to := lvl + 1
		if to > t.maxLvl {
			to = t.maxLvl // 最大层的产物仍留在最大层
		}
		merged := t.mergeLocked(to, olds) // 合并时 olds 仍在层中，墓碑判定含参与者
		t.levels[lvl] = append([]seg.Segment(nil), t.levels[lvl][t.fanout:]...)
		t.levels[to] = append(t.levels[to], merged)
	}
}

// mergeLocked 合并 olds：同键取段号最大者；墓碑仅当全系统所有段中该键无任何 Put 记录时才丢弃。
func (t *Tree) mergeLocked(level int, olds []seg.Segment) seg.Segment {
	latest := map[string]seg.Record{}
	for _, s := range olds {
		for _, r := range s.Recs {
			latest[r.Key] = r
		}
	}
	out := seg.Segment{ID: t.nextID, Level: level}
	t.nextID++
	for _, k := range slices.Sorted(maps.Keys(latest)) {
		if r := latest[k]; !r.Del || t.hasPutLocked(k) {
			out.Recs = append(out.Recs, r)
		}
	}
	return out
}

func (t *Tree) hasPutLocked(key string) bool {
	for _, lv := range t.levels {
		for _, s := range lv {
			for _, r := range s.Recs {
				if r.Key == key && !r.Del {
					return true
				}
			}
		}
	}
	return false
}

// Get 取 memtable 与所有段中该键段号最大的记录；墓碑或查不到返回 false；RLock 保证不混两代段。
func (t *Tree) Get(key string) (string, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	best, bestID, found := seg.Record{}, 0, false
	if r, ok := (seg.Segment{Recs: t.mem}).Latest(key); ok {
		best, bestID, found = r, t.nextID, true
	}
	for _, lv := range t.levels {
		for _, s := range lv {
			if r, ok := s.Latest(key); ok && (!found || s.ID > bestID) {
				best, bestID, found = r, s.ID, true
			}
		}
	}
	if !found || best.Del {
		return "", false
	}
	return best.Val, true
}

// CheckStructure 校验结构不变量：段号唯一递增、每层段数 < fanout、层号 <= maxLevel。
func (t *Tree) CheckStructure() error {
	t.mu.RLock()
	defer t.mu.RUnlock()
	seen := map[int]bool{}
	for lvl, lv := range t.levels {
		for _, s := range lv {
			if len(lv) >= t.fanout || s.ID <= 0 || s.ID >= t.nextID || seen[s.ID] || s.Level != lvl {
				return errors.New("tree: structure invariant violated")
			}
			seen[s.ID] = true
		}
	}
	return nil
}

// Segments 返回全部段的快照（拷贝），供自检与演示观察层分布与段内容。
func (t *Tree) Segments() []seg.Segment {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []seg.Segment
	for _, lv := range t.levels {
		out = append(out, lv...)
	}
	return out
}
