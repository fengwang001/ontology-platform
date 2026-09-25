// Package prune 在有序分区表上执行 静态(p)→动态(v) 裁剪。依赖 part。
package prune

import (
	"errors"
	"sort"
	"strconv"
	"sync/atomic"

	"ontology/part"
)

var (
	// ErrInvalidPredicate 谓词非法：P_lo >= P_hi 或 V_lo >= V_hi。
	ErrInvalidPredicate = errors.New("prune: invalid predicate")
	// ErrUnknownPartition 裁剪引用了未注册的分区 id。
	ErrUnknownPartition = errors.New("prune: unknown partition id")
)

// Predicate 是合取范围谓词 p∈[Plo,Phi) 且 v∈[Vlo,Vhi)。
type Predicate struct {
	Plo, Phi, Vlo, Vhi int64
}

func (q Predicate) valid() bool { return q.Plo < q.Phi && q.Vlo < q.Vhi }

// Table 是按 lo 升序、互不重叠的分区表。
type Table struct {
	parts []part.Partition
	index map[string]int
	// checked：一次 Query 中被检查过的分区个数（非导出，仅供同包测试直读）。
	checked atomic.Int64
}

// NewTable 创建空表。
func NewTable() *Table {
	return &Table{index: map[string]int{}}
}

// Add 加入分区并保持按 Lo 有序；重复 id 或范围重叠视为非法分区。
func (t *Table) Add(p part.Partition) error {
	if _, dup := t.index[p.ID]; dup {
		return part.ErrInvalidPartition
	}
	i := sort.Search(len(t.parts), func(i int) bool { return t.parts[i].Lo >= p.Lo })
	if i > 0 && t.parts[i-1].Hi > p.Lo {
		return part.ErrInvalidPartition
	}
	if i < len(t.parts) && p.Hi > t.parts[i].Lo {
		return part.ErrInvalidPartition
	}
	t.parts = append(t.parts, part.Partition{})
	copy(t.parts[i+1:], t.parts[i:])
	t.parts[i] = p
	for j := i; j < len(t.parts); j++ {
		t.index[t.parts[j].ID] = j
	}
	return nil
}

// classify 对候选分区做静态+动态裁剪（Query 的候选已过静态二分，静态判定为冗余但无害），
// 返回扫描 id 与裁剪 id。
func (t *Table) classify(ids []int, q Predicate) (scan, pruned []string) {
	scan, pruned = []string{}, []string{}
	for _, i := range ids {
		t.checked.Add(1)
		p := t.parts[i]
		if p.StaticPrune(q.Plo, q.Phi) || p.DynamicPrune(q.Vlo, q.Vhi) {
			pruned = append(pruned, p.ID)
		} else {
			scan = append(scan, p.ID)
		}
	}
	return scan, pruned
}

// Query 执行静态→动态裁剪，返回扫描集与裁剪集（均按 lo 升序）。
func (t *Table) Query(q Predicate) (scan, pruned []string, err error) {
	if !q.valid() {
		return nil, nil, ErrInvalidPredicate
	}
	n := len(t.parts)
	// 两次二分定位 p 维相交区间 [lo1,lo2)，区间外的分区零检查直接静态裁剪。
	lo1 := sort.Search(n, func(i int) bool { return t.parts[i].Hi > q.Plo })
	lo2 := sort.Search(n, func(i int) bool { return t.parts[i].Lo >= q.Phi })
	t.checked.Store(0)
	scan, dynPruned := t.classify(indexRange(lo1, lo2), q)
	pruned = make([]string, 0, lo1+n-lo2+len(dynPruned))
	for i := 0; i < lo1; i++ {
		pruned = append(pruned, t.parts[i].ID)
	}
	pruned = append(pruned, dynPruned...)
	for i := lo2; i < n; i++ {
		pruned = append(pruned, t.parts[i].ID)
	}
	return scan, pruned, nil
}

// QueryRefs 仅裁剪 refs 引用的分区；出现未注册 id 整体失败、不出结果。
func (t *Table) QueryRefs(refs []string, q Predicate) (scan, pruned []string, err error) {
	if !q.valid() {
		return nil, nil, ErrInvalidPredicate
	}
	idx := make([]int, len(refs))
	for i, id := range refs {
		j, ok := t.index[id]
		if !ok {
			return nil, nil, ErrUnknownPartition
		}
		idx[i] = j
	}
	sort.Ints(idx)
	t.checked.Store(0)
	scan, pruned = t.classify(idx, q)
	return scan, pruned, nil
}

func indexRange(a, b int) []int {
	r := make([]int, b-a)
	for i := range r {
		r[i] = a + i
	}
	return r
}

// CheckSublinear 内部核验：窄谓词下被检查分区数不随 N 线性增长。
// 只返回结论布尔值，计数器数值不跨包暴露。
func CheckSublinear() bool {
	for _, n := range []int{100, 1000, 10000} {
		t := NewTable()
		for i := 0; i < n; i++ {
			p, err := part.New(idOf(i), int64(i*10), int64(i*10+10), 0, 1)
			if err != nil || t.Add(p) != nil {
				return false
			}
		}
		// 窄谓词：只与 [0,10)、[10,20) 两个分区的 p 范围相交。
		scan, _, err := t.Query(Predicate{Plo: 5, Phi: 15, Vlo: 0, Vhi: 2})
		if err != nil {
			return false
		}
		if got := int(t.checked.Load()); got > len(scan)+2 {
			return false
		}
	}
	return true
}

func idOf(i int) string { return "p" + strconv.Itoa(i) }
