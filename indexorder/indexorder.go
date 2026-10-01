package indexorder

import (
	"errors"
	"fmt"
	"sync"
)

// Direction 表示索引列项或 ORDER BY 列项的排序方向。
type Direction int

const (
	// Asc 升序。
	Asc Direction = 1
	// Desc 降序。
	Desc Direction = 2
)

// NullsPos 表示空值在排序中的位置。
type NullsPos int

const (
	// NullsFirst 空值排在最前。
	NullsFirst NullsPos = 1
	// NullsLast 空值排在最后。
	NullsLast NullsPos = 2
)

// ScanDirection 表示扫描索引的方向。
type ScanDirection int

const (
	// Forward 正向扫描。
	Forward ScanDirection = 1
	// Backward 反向扫描。
	Backward ScanDirection = 2
)

// ColumnItem 是索引或 ORDER BY 中的一个列项。
type ColumnItem struct {
	Column string
	Dir    Direction
	Nulls  NullsPos
}

// Index 是一个已登记的索引：名字 + 列项序列。
type Index struct {
	Name  string
	Items []ColumnItem
}

// 各类可区分的错误原因，可用 errors.Is 判定。
var (
	ErrEmptyName             = errors.New("indexorder: 索引名字为空")
	ErrDuplicateName         = errors.New("indexorder: 索引名字已存在")
	ErrEmptyItems            = errors.New("indexorder: 列项序列为空")
	ErrEmptyColumn           = errors.New("indexorder: 列名为空")
	ErrInvalidDirection      = errors.New("indexorder: 方向非法")
	ErrInvalidNulls          = errors.New("indexorder: 空值位置非法")
	ErrDuplicateColumn       = errors.New("indexorder: 同一索引内列名重复")
	ErrIndexNotFound         = errors.New("indexorder: 索引不存在")
	ErrEmptyEqColumn         = errors.New("indexorder: eq 含空列名")
	ErrEmptyOrderColumn      = errors.New("indexorder: order 的列名为空")
	ErrInvalidOrderDirection = errors.New("indexorder: order 的方向非法")
	ErrInvalidOrderNulls     = errors.New("indexorder: order 的空值位置非法")
	ErrNoSatisfyingIndex     = errors.New("indexorder: 没有任何已登记索引满足")
)

func validDirection(d Direction) bool { return d == Asc || d == Desc }

func validNulls(n NullsPos) bool { return n == NullsFirst || n == NullsLast }

func flipDirection(d Direction) Direction {
	if d == Asc {
		return Desc
	}
	return Asc
}

func flipNulls(n NullsPos) NullsPos {
	if n == NullsFirst {
		return NullsLast
	}
	return NullsFirst
}

// Registry 保存已登记的索引集合，所有方法可并发调用，
// 结果等价于某个串行顺序。
type Registry struct {
	mu      sync.RWMutex
	indexes map[string]Index
}

// NewRegistry 创建一个空的登记器。
func NewRegistry() *Registry {
	return &Registry{indexes: make(map[string]Index)}
}

// Register 登记一个索引。校验失败时按如下顺序只报第一个错误：
// 名字为空、名字已存在、列项序列为空、某列名为空、
// 某列项的方向或空值位置非法、同一索引内列名重复。
// 被拒绝时不改变已登记的索引集合。
func (r *Registry) Register(idx Index) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if idx.Name == "" {
		return ErrEmptyName
	}
	if _, ok := r.indexes[idx.Name]; ok {
		return fmt.Errorf("%w: %q", ErrDuplicateName, idx.Name)
	}
	if len(idx.Items) == 0 {
		return ErrEmptyItems
	}
	for _, it := range idx.Items {
		if it.Column == "" {
			return ErrEmptyColumn
		}
	}
	for _, it := range idx.Items {
		if !validDirection(it.Dir) {
			return fmt.Errorf("%w: 列 %q", ErrInvalidDirection, it.Column)
		}
		if !validNulls(it.Nulls) {
			return fmt.Errorf("%w: 列 %q", ErrInvalidNulls, it.Column)
		}
	}
	seen := make(map[string]struct{}, len(idx.Items))
	for _, it := range idx.Items {
		if _, ok := seen[it.Column]; ok {
			return fmt.Errorf("%w: %q", ErrDuplicateColumn, it.Column)
		}
		seen[it.Column] = struct{}{}
	}

	items := make([]ColumnItem, len(idx.Items))
	copy(items, idx.Items)
	r.indexes[idx.Name] = Index{Name: idx.Name, Items: items}
	return nil
}

// Drop 删除指定名字的索引；名字不存在时报 ErrIndexNotFound，
// 被拒绝时不改变已登记的索引集合。
func (r *Registry) Drop(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.indexes[name]; !ok {
		return fmt.Errorf("%w: %q", ErrIndexNotFound, name)
	}
	delete(r.indexes, name)
	return nil
}

// Choose 为等值列集合 eq 与 ORDER BY 列表 order 挑选能免去排序的
// 索引与扫描方向。Choose 为只读操作。
//
// 先规整 order 得到 need：丢弃列名属于 eq 的项，再对重复出现的列名
// 只保留第一次出现的项。need 为空时任何索引都以正向满足。
//
// 多个索引都能满足时的选择次序：能以正向满足者优先于只能以反向
// 满足者，再取列项数少者，再取名字字节序小者。
func (r *Registry) Choose(eq []string, order []ColumnItem) (string, ScanDirection, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, c := range eq {
		if c == "" {
			return "", 0, ErrEmptyEqColumn
		}
	}
	for _, it := range order {
		if it.Column == "" {
			return "", 0, ErrEmptyOrderColumn
		}
	}
	for _, it := range order {
		if !validDirection(it.Dir) {
			return "", 0, fmt.Errorf("%w: 列 %q", ErrInvalidOrderDirection, it.Column)
		}
		if !validNulls(it.Nulls) {
			return "", 0, fmt.Errorf("%w: 列 %q", ErrInvalidOrderNulls, it.Column)
		}
	}

	eqSet := make(map[string]struct{}, len(eq))
	for _, c := range eq {
		eqSet[c] = struct{}{}
	}
	need := normalizeOrder(eqSet, order)

	var best *candidate
	for name, idx := range r.indexes {
		var dir ScanDirection
		switch {
		case satisfies(idx.Items, eqSet, need, Forward):
			dir = Forward
		case satisfies(idx.Items, eqSet, need, Backward):
			dir = Backward
		default:
			continue
		}
		c := candidate{name: name, dir: dir, nItem: len(idx.Items)}
		if best == nil || lessCandidate(c, *best) {
			best = &c
		}
	}
	if best == nil {
		return "", 0, ErrNoSatisfyingIndex
	}
	return best.name, best.dir, nil
}

// candidate 是一个可满足查询的索引及其扫描方向。
type candidate struct {
	name  string
	dir   ScanDirection
	nItem int
}

// lessCandidate 实现选择次序：正向优先，再列项数少者优先，
// 再名字字节序小者优先。
func lessCandidate(a, b candidate) bool {
	if a.dir != b.dir {
		return a.dir == Forward
	}
	if a.nItem != b.nItem {
		return a.nItem < b.nItem
	}
	return a.name < b.name
}

// normalizeOrder 规整 ORDER BY：丢弃列名属于 eq 的项，再对重复
// 出现的列名只保留第一次出现的项。
func normalizeOrder(eqSet map[string]struct{}, order []ColumnItem) []ColumnItem {
	seen := make(map[string]struct{}, len(order))
	var need []ColumnItem
	for _, it := range order {
		if _, ok := eqSet[it.Column]; ok {
			continue
		}
		if _, ok := seen[it.Column]; ok {
			continue
		}
		seen[it.Column] = struct{}{}
		need = append(need, it)
	}
	return need
}

// satisfies 判定索引列项序列 items 以方向 dir 是否满足 need。
// 列名属于 eq 的索引列项被跳过；其余列项必须与 need 中下一个
// 未匹配项列名相同，且正向时方向与空值位置都相同，反向时都与
// need 项的取反相同。need 全部匹配后立即满足。
func satisfies(items []ColumnItem, eqSet map[string]struct{}, need []ColumnItem, dir ScanDirection) bool {
	next := 0
	for _, c := range items {
		if next == len(need) {
			return true
		}
		if _, ok := eqSet[c.Column]; ok {
			continue
		}
		n := need[next]
		if c.Column != n.Column {
			return false
		}
		wantDir, wantNulls := n.Dir, n.Nulls
		if dir == Backward {
			wantDir, wantNulls = flipDirection(n.Dir), flipNulls(n.Nulls)
		}
		if c.Dir != wantDir || c.Nulls != wantNulls {
			return false
		}
		next++
	}
	return next == len(need)
}
