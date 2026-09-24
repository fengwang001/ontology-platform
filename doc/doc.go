// Package doc 在 rga 树之上维护文档状态与操作日志，提供并发安全的
// Insert/Delete/Text。它只依赖 rga。朴素重算与自检见 selfcheck.go。
package doc

import (
	"sort"
	"strings"
	"sync"

	"ontology/rga"
)

// ID 与 Empty 直接复用 rga 的定义。
type ID = rga.ID

var Empty = rga.Empty

func id(ts uint64, rep string) ID { return ID{Lamport: ts, Replica: rep} }

// 可判定哨兵错误（与 rga 同一组，彼此互不相同）。
var (
	ErrInvalidID      = rga.ErrInvalidID
	ErrDuplicateID    = rga.ErrDuplicateID
	ErrPrevNotFound   = rga.ErrPrevNotFound
	ErrIDNotFound     = rga.ErrIDNotFound
	ErrAlreadyDeleted = rga.ErrAlreadyDeleted
)

// rec 是已接受操作的仅追加日志，供朴素参照重算。
type rec struct {
	insert   bool
	prev, id ID
	ch       rune
}

// Doc 是一份进程内协同文本。
type Doc struct {
	mu sync.RWMutex
	tr *rga.Tree

	// lastProbe 记录最近一次被接受的 Insert 定位 prev 时检查过的元素个数：
	// map 哈希定位，∅ 为 0、命中已有元素为 1，与文档规模无关。非导出。
	lastProbe int
	log       []rec
}

func New() *Doc { return &Doc{tr: rga.NewTree()} }

// Insert 先经 rga 做全部校验，通过后才记日志并更新探针；被拒则状态不变。
func (d *Doc) Insert(prev, x ID, ch rune) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.tr.Add(prev, x, ch); err != nil {
		return err
	}
	d.log = append(d.log, rec{insert: true, prev: prev, id: x, ch: ch})
	if prev == rga.Empty {
		d.lastProbe = 0
	} else {
		d.lastProbe = 1
	}
	return nil
}

// Delete 仅打墓碑。
func (d *Doc) Delete(x ID) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.tr.Delete(x); err != nil {
		return err
	}
	d.log = append(d.log, rec{id: x})
	return nil
}

// snapshot 返回操作日志副本与当前可见文本（短暂持锁）。
func (d *Doc) snapshot() ([]rec, string) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return append([]rec(nil), d.log...), d.tr.Visible()
}

// Text 并发只读。
func (d *Doc) Text() string {
	_, s := d.snapshot()
	return s
}

// greater 是独立于 rga 的参照全序：lamport 降序，tie 时 replica 降序。
func greater(a, b ID) bool {
	if a.Lamport != b.Lamport {
		return a.Lamport > b.Lamport
	}
	return a.Replica > b.Replica
}

// naiveText 是朴素参照：从操作日志批量重建父子关系，每组兄弟按 ID 降序，
// DFS 跳过墓碑字符但仍递归子孙。
func naiveText(rs []rec) string {
	type cell struct {
		ch   rune
		prev ID
		dead bool
	}
	m := map[ID]*cell{}
	for _, r := range rs {
		if r.insert {
			m[r.id] = &cell{ch: r.ch, prev: r.prev}
		} else {
			m[r.id].dead = true
		}
	}
	kids := map[ID][]ID{}
	var roots []ID
	for x := range m {
		if m[x].prev == rga.Empty {
			roots = append(roots, x)
		} else {
			kids[m[x].prev] = append(kids[m[x].prev], x)
		}
	}
	ord := func(s []ID) { sort.Slice(s, func(i, j int) bool { return greater(s[i], s[j]) }) }
	for p := range kids {
		ord(kids[p])
	}
	ord(roots)
	var b strings.Builder
	var walk func(ID)
	walk = func(x ID) {
		c := m[x]
		if !c.dead {
			b.WriteRune(c.ch)
		}
		for _, k := range kids[x] {
			walk(k)
		}
	}
	for _, r := range roots {
		walk(r)
	}
	return b.String()
}
