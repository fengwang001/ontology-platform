// Package srec 维护主记录与溢出表的一致性：写新块→更新引用→回收旧块。
package srec

import (
	"errors"
	"sort"

	"ontology/spill"
)

var (
	ErrInvalidParam  = errors.New("srec: 阈值或上限非法")
	ErrEmptyKey      = errors.New("srec: 空 key")
	ErrOverflowLimit = errors.New("srec: 溢出块数超限")
	ErrDanglingRef   = errors.New("srec: 存在悬挂引用")
)

// Table 主记录 + 溢出表。traversed 记录最近一次 Put/Del 为定位旧块
// 逐个遍历过的溢出表条目数（非导出，不出现在公开接口）。
type Table struct {
	st        *spill.Store
	max       int
	traversed int
}

func New(t, maxOverflow int) (*Table, error) {
	if t <= 0 || maxOverflow <= 0 {
		return nil, ErrInvalidParam
	}
	return &Table{st: spill.NewStore(t), max: maxOverflow}, nil
}

// Put 顺序铁律：先写新块 → 再更新引用 → 最后回收旧块。
// 任何校验失败都在改动状态之前返回，拒绝不留痕。
func (tb *Table) Put(k string, v []byte) error {
	if k == "" {
		return ErrEmptyKey
	}
	st := tb.st
	tb.traversed = 0 // 旧块靠 ref[k] 直达，不遍历溢出表
	old, hadRef := st.Ref[k]
	if spill.IsInline(len(v), st.T) {
		st.Inline[k] = v
		delete(st.Ref, k)
		if hadRef {
			delete(st.Blocks, old)
		}
		return nil
	}
	if !hadRef && len(st.Blocks)+1 > tb.max {
		return ErrOverflowLimit
	}
	bid := st.Alloc()
	st.Blocks[bid] = v // 1. 先写新块
	st.Ref[k] = bid    // 2. 再更新引用
	delete(st.Inline, k)
	if hadRef {
		delete(st.Blocks, old) // 3. 最后回收旧块
	}
	return nil
}

func (tb *Table) Get(k string) ([]byte, bool, error) {
	if k == "" {
		return nil, false, ErrEmptyKey
	}
	if v, ok := tb.st.Inline[k]; ok {
		return v, true, nil
	}
	if bid, ok := tb.st.Ref[k]; ok {
		return tb.st.Blocks[bid], true, nil
	}
	return nil, false, nil
}

func (tb *Table) Del(k string) error {
	if k == "" {
		return ErrEmptyKey
	}
	tb.traversed = 0
	if bid, ok := tb.st.Ref[k]; ok {
		delete(tb.st.Blocks, bid)
		delete(tb.st.Ref, k)
	}
	delete(tb.st.Inline, k)
	return nil
}

// Recover 回收所有孤儿块；发现悬挂引用则列出 key 并返回 ErrDanglingRef。
func (tb *Table) Recover() (int, []string, error) {
	used := map[uint64]bool{}
	var dangling []string
	for k, bid := range tb.st.Ref {
		if _, ok := tb.st.Blocks[bid]; !ok {
			dangling = append(dangling, k)
		}
		used[bid] = true
	}
	reclaimed := 0
	for bid := range tb.st.Blocks {
		if !used[bid] {
			delete(tb.st.Blocks, bid)
			reclaimed++
		}
	}
	if len(dangling) > 0 {
		sort.Strings(dangling)
		return reclaimed, dangling, ErrDanglingRef
	}
	return reclaimed, nil, nil
}

func (tb *Table) OverflowBlocks() int { return len(tb.st.Blocks) }

// Raw 暴露底层状态，仅供自检/演示/测试做故障注入。
func (tb *Table) Raw() *spill.Store { return tb.st }

// Check 校验引用完整：每个 ref 指向存在的块，每个块恰被一个 ref 引用。
func (tb *Table) Check() error {
	seen := map[uint64]int{}
	for k, bid := range tb.st.Ref {
		if _, ok := tb.st.Blocks[bid]; !ok {
			return ErrDanglingRef
		}
		seen[bid]++
		_ = k
	}
	for bid := range tb.st.Blocks {
		if seen[bid] != 1 {
			return errors.New("srec: 孤儿块或重复引用")
		}
	}
	return nil
}
