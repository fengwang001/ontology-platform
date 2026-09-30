package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// PKColumn 是隐含的整数主键列名。
const PKColumn = "id"

// indexEntry 是二级索引中的一个条目，按 (key, pk) 字典序排序。
type indexEntry struct {
	key int64
	pk  int64
}

// Table 是带一个有序二级索引的内存表。
// 所有列（除主键外）均为 int64；索引列可声明唯一。
// 读操作持有 RLock，更新语句持有 Lock，
// 因此并发读者只能看到某条语句之前或之后的完整表。
type Table struct {
	mu          sync.RWMutex
	columns     map[string]struct{}
	indexColumn string
	unique      bool
	rows        map[int64]map[string]int64
	index       []indexEntry // 始终按 (key, pk) 升序
}

// NewTable 创建一张表。columns 列出除主键外的全部列，indexColumn 必须包含其中。
func NewTable(indexColumn string, unique bool, columns ...string) (*Table, error) {
	cols := make(map[string]struct{}, len(columns))
	for _, c := range columns {
		if c == "" || c == PKColumn {
			return nil, fmt.Errorf("非法列名 %q", c)
		}
		if _, dup := cols[c]; dup {
			return nil, fmt.Errorf("重复列名 %q", c)
		}
		cols[c] = struct{}{}
	}
	if _, ok := cols[indexColumn]; !ok {
		return nil, fmt.Errorf("索引列 %q 不在表结构中", indexColumn)
	}
	return &Table{
		columns:     cols,
		indexColumn: indexColumn,
		unique:      unique,
		rows:        make(map[int64]map[string]int64),
	}, nil
}

// IndexColumn 返回索引列名。
func (t *Table) IndexColumn() string { return t.indexColumn }

// Insert 插入一行；values 必须恰好覆盖表结构中的全部列。
// 建表阶段的插入立即检查唯一性。
func (t *Table) Insert(pk int64, values map[string]int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.rows[pk]; ok {
		return fmt.Errorf("主键 %d 已存在", pk)
	}
	row := make(map[string]int64, len(t.columns))
	for c := range t.columns {
		v, ok := values[c]
		if !ok {
			return fmt.Errorf("缺少列 %q", c)
		}
		row[c] = v
	}
	for c := range values {
		if _, ok := t.columns[c]; !ok {
			return fmt.Errorf("%w: %q", ErrUnknownColumn, c)
		}
	}
	if t.unique && t.keyExists(row[t.indexColumn]) {
		return fmt.Errorf("%w: 插入时键 %d 已存在", ErrUniqueViolation, row[t.indexColumn])
	}
	t.rows[pk] = row
	t.insertEntry(row[t.indexColumn], pk)
	return nil
}

// Snapshot 返回整张表的深拷贝（pk -> 列值）。并发读者借此观察语句前后态。
func (t *Table) Snapshot() map[int64]map[string]int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[int64]map[string]int64, len(t.rows))
	for pk, row := range t.rows {
		cp := make(map[string]int64, len(row))
		for c, v := range row {
			cp[c] = v
		}
		out[pk] = cp
	}
	return out
}

// CheckInvariants 校验：每一行在索引中恰好出现一次，且索引键与行值一致。
func (t *Table) CheckInvariants() error {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if len(t.index) != len(t.rows) {
		return fmt.Errorf("索引条目数 %d 与行数 %d 不一致", len(t.index), len(t.rows))
	}
	for i := 1; i < len(t.index); i++ {
		a, b := t.index[i-1], t.index[i]
		if a.key > b.key || (a.key == b.key && a.pk >= b.pk) {
			return fmt.Errorf("索引顺序被破坏: %+v 后接 %+v", a, b)
		}
	}
	seen := make(map[int64]int, len(t.rows))
	for _, e := range t.index {
		seen[e.pk]++
		row, ok := t.rows[e.pk]
		if !ok {
			return fmt.Errorf("索引条目指向不存在的行 pk=%d", e.pk)
		}
		if row[t.indexColumn] != e.key {
			return fmt.Errorf("行 pk=%d 的索引键 %d 与行值 %d 不一致", e.pk, e.key, row[t.indexColumn])
		}
	}
	for pk := range t.rows {
		if seen[pk] != 1 {
			return fmt.Errorf("行 pk=%d 在索引中出现 %d 次", pk, seen[pk])
		}
	}
	return nil
}

// search 返回 (key, pk) 应处的位置及是否已存在。
func (t *Table) search(key, pk int64) (int, bool) {
	i := sort.Search(len(t.index), func(i int) bool {
		e := t.index[i]
		return e.key > key || (e.key == key && e.pk >= pk)
	})
	return i, i < len(t.index) && t.index[i].key == key && t.index[i].pk == pk
}

func (t *Table) keyExists(key int64) bool {
	i := sort.Search(len(t.index), func(i int) bool { return t.index[i].key >= key })
	return i < len(t.index) && t.index[i].key == key
}

func (t *Table) insertEntry(key, pk int64) {
	i, _ := t.search(key, pk)
	t.index = append(t.index, indexEntry{})
	copy(t.index[i+1:], t.index[i:])
	t.index[i] = indexEntry{key: key, pk: pk}
}

func (t *Table) removeEntry(key, pk int64) {
	i, found := t.search(key, pk)
	if !found {
		panic(fmt.Sprintf("索引条目 (%d, %d) 不存在", key, pk))
	}
	copy(t.index[i:], t.index[i+1:])
	t.index = t.index[:len(t.index)-1]
}

// scanRange 沿索引顺序返回键落在 [lo, hi) 内的全部条目。
// 返回的条目数是本次扫描考察的条目数。
func (t *Table) scanRange(lo, hi int64) []indexEntry {
	i := sort.Search(len(t.index), func(i int) bool { return t.index[i].key >= lo })
	var out []indexEntry
	for ; i < len(t.index) && t.index[i].key < hi; i++ {
		out = append(out, t.index[i])
	}
	return out
}
