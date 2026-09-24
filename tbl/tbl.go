// Package tbl 记录表（pk -> f）与 Put/Del 的「删旧插新」原子索引维护，依赖 ient。
package tbl

import (
	"errors"
	"fmt"

	"ontology/ient"
)

// 三类可判定且互不相同的哨兵错误。
var (
	ErrEmptyPK  = errors.New("tbl: empty primary key")
	ErrNotFound = errors.New("tbl: primary key not found")
	ErrBatch    = errors.New("tbl: batch rejected")
)

// Op 是一条写操作：Del 为真表示 Del(PK)，否则表示 Put(PK, F)。
type Op struct {
	Del bool
	PK  string
	F   int64
}

// T 是记录表：主键到 F 的映射，并维护 F 上的二级索引。
type T struct {
	rec map[string]int64
	idx *ient.Set
}

// New 返回空记录表。
func New() *T { return &T{rec: make(map[string]int64), idx: &ient.Set{}} }

// put 插入新主键，或对已有主键做索引键更新：先删旧组项再插新组项；
// 新 F 与旧 F 相等时是无操作，不产生重复索引项。
func (t *T) put(pk string, f int64) {
	if old, ok := t.rec[pk]; ok {
		if old == f {
			return
		}
		t.idx.Delete(old, pk)
	}
	t.idx.Insert(f, pk)
	t.rec[pk] = f
}

// del 删除主键及其索引项，调用方保证主键存在。
func (t *T) del(pk string) {
	t.idx.Delete(t.rec[pk], pk)
	delete(t.rec, pk)
}

// Put 插入新主键或更新已有主键的 F；空主键是错误。
func (t *T) Put(pk string, f int64) error {
	if pk == "" {
		return ErrEmptyPK
	}
	t.put(pk, f)
	return nil
}

// Del 删除主键及其索引项；空主键或主键不存在是错误。
func (t *T) Del(pk string) error {
	if pk == "" {
		return ErrEmptyPK
	}
	if _, ok := t.rec[pk]; !ok {
		return ErrNotFound
	}
	t.del(pk)
	return nil
}

// Apply 先在副本上整批预演：任一条非法则整批不生效，
// 记录表与索引全部不变；全部合法才落到真实状态。
// 多操作批次被拒时错误包有 ErrBatch 标记，与单操作的裸哨兵错误相区分。
func (t *T) Apply(ops []Op) error {
	reject := func(err error) error {
		if len(ops) > 1 {
			return fmt.Errorf("%w: %w", ErrBatch, err)
		}
		return err
	}
	sim := make(map[string]int64, len(t.rec)+len(ops))
	for k, v := range t.rec {
		sim[k] = v
	}
	for _, op := range ops {
		if op.PK == "" {
			return reject(ErrEmptyPK)
		}
		if op.Del {
			if _, ok := sim[op.PK]; !ok {
				return reject(ErrNotFound)
			}
			delete(sim, op.PK)
		} else {
			sim[op.PK] = op.F
		}
	}
	for _, op := range ops {
		if op.Del {
			t.del(op.PK)
		} else {
			t.put(op.PK, op.F)
		}
	}
	return nil
}

// Eq 返回 F==f 的主键列表，按主键升序。
func (t *T) Eq(f int64) []string { return t.idx.Eq(f) }

// Range 返回 F 落在 [lo, hi) 内的主键列表，按主键升序。
func (t *T) Range(lo, hi int64) []string { return t.idx.Range(lo, hi) }
