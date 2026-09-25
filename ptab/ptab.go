// Package ptab 行存储与 Put/Delete 的命中迁移逻辑，调用 pindex 维护索引。
package ptab

import (
	"errors"

	"ontology/pindex"
)

// 可判定哨兵错误，三者互不相同。
var (
	ErrBadID    = errors.New("ptab: negative id")
	ErrBadScore = errors.New("ptab: score out of range [0,100]")
	ErrNotFound = errors.New("ptab: row not found")
)

// Row 是一行的最新值。
type Row struct {
	Key   int
	Score int
}

// Table 是 id -> Row 的全表，附带过滤索引。
type Table struct {
	rows map[int]Row
	idx  *pindex.Index
}

// New 以给定索引建空表。
func New(idx *pindex.Index) *Table {
	return &Table{rows: make(map[int]Row), idx: idx}
}

// Put 幂等 upsert。先校验后改状态：被拒时不留任何痕迹。
// 命中迁移：旧新都命中但 Key 变了→迁移；旧命中新不中→移除；旧不中新命中→加入。
func (t *Table) Put(id, key, score int) error {
	if id < 0 {
		return ErrBadID
	}
	if score < 0 || score > 100 {
		return ErrBadScore
	}
	old, ok := t.rows[id]
	if ok {
		oldHit, newHit := pindex.Hit(old.Score), pindex.Hit(score)
		switch {
		case oldHit && newHit && old.Key != key:
			t.idx.Remove(old.Key, id)
			t.idx.Add(key, id)
		case oldHit && !newHit:
			t.idx.Remove(old.Key, id)
		case !oldHit && newHit:
			t.idx.Add(key, id)
		}
	} else if pindex.Hit(score) {
		t.idx.Add(key, id)
	}
	t.rows[id] = Row{Key: key, Score: score}
	return nil
}

// Delete 删除一行；命中谓词则从索引移除；不存在返回 ErrNotFound 且状态不变。
func (t *Table) Delete(id int) error {
	old, ok := t.rows[id]
	if !ok {
		return ErrNotFound
	}
	if pindex.Hit(old.Score) {
		t.idx.Remove(old.Key, id)
	}
	delete(t.rows, id)
	return nil
}

// Snapshot 返回全表副本。
func (t *Table) Snapshot() map[int]Row {
	out := make(map[int]Row, len(t.rows))
	for id, r := range t.rows {
		out[id] = r
	}
	return out
}
