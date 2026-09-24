// Package api 对外接口：并发安全的记录表 + 单字段二级索引，依赖 tbl。
package api

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sync"

	"ontology/tbl"
)

// Op 是一条写操作。
type Op = tbl.Op

var (
	ErrEmptyPK  = tbl.ErrEmptyPK
	ErrNotFound = tbl.ErrNotFound
	ErrBatch    = tbl.ErrBatch
)

// Put 构造写入操作；Del 构造删除操作。
func Put(pk string, f int64) Op { return Op{PK: pk, F: f} }
func Del(pk string) Op          { return Op{Del: true, PK: pk} }

// DB 是并发安全的记录表句柄。
type DB struct {
	mu sync.RWMutex
	t  *tbl.T
}

// New 返回空实例。
func New() *DB { return &DB{t: tbl.New()} }

// Apply 整批应用写操作：任一条非法则整批不生效。
func (d *DB) Apply(ops []Op) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.t.Apply(ops)
}

// Eq 返回 F==f 的主键列表，按主键升序。
func (d *DB) Eq(f int64) ([]string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.t.Eq(f), nil
}

// Range 返回 F 落在 [lo, hi) 内的主键列表，按主键升序。
func (d *DB) Range(lo, hi int64) ([]string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.t.Range(lo, hi), nil
}

// modelEq 批量重算：整体重扫模型 map，过滤 F==f 后按主键排序。
func modelEq(m map[string]int64, f int64) []string {
	var out []string
	for k, v := range m {
		if v == f {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (d *DB) SelfCheck() error {
	db := New()
	chk := func(lo, hi int64, want []string) error {
		got, _ := db.Range(lo, hi)
		if !slices.Equal(got, want) {
			return fmt.Errorf("selfcheck: range(%d,%d)=%v want %v", lo, hi, got, want)
		}
		return nil
	}
	// 不变量 2、3：八步序列，逐步核验两个索引组与两次查询。
	steps := []struct {
		op       Op
		eq5, eq8 []string
	}{
		{Put("a", 5), []string{"a"}, nil},
		{Put("b", 5), []string{"a", "b"}, nil},
		{Put("c", 8), []string{"a", "b"}, []string{"c"}},
		{Put("a", 8), []string{"b"}, []string{"a", "c"}},
		{Del("b"), nil, []string{"a", "c"}},
		{Put("d", 5), []string{"d"}, []string{"a", "c"}},
	}
	for i, s := range steps {
		if err := db.Apply([]Op{s.op}); err != nil {
			return fmt.Errorf("selfcheck step %d: %w", i+1, err)
		}
		if err := chk(5, 6, s.eq5); err != nil {
			return err
		}
		if err := chk(8, 9, s.eq8); err != nil {
			return err
		}
	}
	if err := chk(5, 8, []string{"d"}); err != nil { // 第 7 步 Range(5,8)
		return err
	}
	if err := chk(8, 9, []string{"a", "c"}); err != nil { // 第 8 步 Eq(8)
		return err
	}
	db = New() // 不变量 1：换新实例跑确定性随机序列，逐查询与批量重算比对
	r := rand.New(rand.NewSource(1))
	model := map[string]int64{}
	keys := []string{"a", "b", "c", "d", "e"}
	for i := 0; i < 300; i++ {
		op := Put(keys[r.Intn(5)], int64(r.Intn(4)))
		if r.Intn(3) == 0 {
			op = Del(keys[r.Intn(5)])
		}
		_, ok := model[op.PK]
		err := db.Apply([]Op{op})
		if op.Del && !ok {
			if !errors.Is(err, ErrNotFound) {
				return fmt.Errorf("selfcheck: del missing key err=%v", err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("selfcheck: %w", err)
		}
		if op.Del {
			delete(model, op.PK)
		} else {
			model[op.PK] = op.F
		}
	}
	for f := int64(0); f < 4; f++ {
		got, _ := db.Eq(f)
		if !slices.Equal(got, modelEq(model, f)) {
			return fmt.Errorf("selfcheck: eq(%d) inconsistent with batch recompute", f)
		}
	}
	// 不变量 4：被拒批次不留痕，之后仍可正常使用。
	before, _ := db.Range(-10, 100)
	if err := db.Apply([]Op{Put("z", 1), Del("no-such")}); !errors.Is(err, ErrBatch) {
		return fmt.Errorf("selfcheck: bad batch err=%v", err)
	}
	after, _ := db.Range(-10, 100)
	if !slices.Equal(before, after) {
		return errors.New("selfcheck: rejected batch changed state")
	}
	return db.Apply([]Op{Put("z", 1)})
}
