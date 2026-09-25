package dd

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/tup"
)

type br struct {
	key, c1 string
	c2      int
}

// assertMatch 把引擎与朴素批量重算全量比对：逐分组 distinct、逐元组引用计数、
// Total（钉住不变量 1 与批量一致、2 计数精确）。
func assertMatch(t *testing.T, e *Engine, rows map[int]br) {
	t.Helper()
	ref := map[string]map[tup.T]int{}
	for _, r := range rows {
		if ref[r.key] == nil {
			ref[r.key] = map[tup.T]int{}
		}
		ref[r.key][tup.T{C1: r.c1, C2: r.c2}]++
	}
	total := 0
	for k, m := range ref {
		total += len(m)
		if e.Distinct(k) != len(m) {
			t.Fatalf("Distinct(%q)=%d batch=%d", k, e.Distinct(k), len(m))
		}
		for x, n := range m {
			if e.cat.RefCount(k, x) != n {
				t.Fatalf("RefCount(%q,%v)=%d batch=%d", k, x, e.cat.RefCount(k, x), n)
			}
		}
	}
	if e.Total() != total {
		t.Fatalf("Total=%d batch=%d", e.Total(), total)
	}
}

// snap 序列化行数、各分组 distinct 与 total，供被拒操作前后比对。
func snap(e *Engine) string {
	return fmt.Sprintf("%d|%d|%d", len(e.rows), e.Distinct("k")+e.Distinct("j"), e.Total())
}

// TestUpsertRetractMatchesBatch：多 seed 随机操作流，每步后与批量重算全量比对；
// 随机混入的三类非法操作须返回对应哨兵错误且可观察状态不变（钉住不变量 1/2/4）。
func TestUpsertRetractMatchesBatch(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			e, rows := NewEngine(), map[int]br{}
			for n := 0; n < 800; n++ {
				id, key := rng.Intn(12)+1, []string{"k", "j", ""}[rng.Intn(3)]
				if rng.Intn(15) == 0 {
					id = -rng.Intn(2)
				}
				c1, c2 := []string{"a", "b", "c"}[rng.Intn(3)], rng.Intn(4)
				switch {
				case id <= 0: // 非法 rowID：Upsert 与 Delete 都必须被拒
					before := snap(e)
					err := e.Upsert(id, key, c1, c2)
					if rng.Intn(2) == 0 {
						err = e.Delete(id)
					}
					if !errors.Is(err, ErrInvalidRowID) || snap(e) != before {
						t.Fatal("bad rowID must leave no trace")
					}
				case rng.Intn(3) == 0: // 删除（含删不存在的行）
					if _, ok := rows[id]; !ok {
						before := snap(e)
						if !errors.Is(e.Delete(id), ErrRowNotFound) || snap(e) != before {
							t.Fatal("missing-row Delete must leave no trace")
						}
						continue
					}
					if err := e.Delete(id); err != nil {
						t.Fatal(err)
					}
					delete(rows, id)
				case key == "":
					before := snap(e)
					if !errors.Is(e.Upsert(id, key, c1, c2), ErrEmptyKey) || snap(e) != before {
						t.Fatal("empty key must leave no trace")
					}
				default:
					if err := e.Upsert(id, key, c1, c2); err != nil {
						t.Fatal(err)
					}
					rows[id] = br{key, c1, c2}
				}
				if id > 0 && key != "" {
					assertMatch(t, e, rows)
				}
			}
		})
	}
}

// TestTupleChecksConstant：m=100/1000/10000 下只触及一个元组的 Upsert/Delete，
// 非导出检查计数恒为 2、1，不随 m 线性增长（第四节；计数器无任何导出读取途径）。
func TestTupleChecksConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		e := NewEngine()
		for i := 1; i <= m; i++ {
			if err := e.Upsert(i, "k", "c", i); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.Upsert(1, "k", "c", 2); err != nil || e.checks != 2 {
			t.Fatalf("m=%d upsert checks=%d", m, e.checks)
		}
		if err := e.Delete(m); err != nil || e.checks != 1 {
			t.Fatalf("m=%d delete checks=%d", m, e.checks)
		}
	}
}

// TestRejectedOperationsLeaveNoTrace：三类哨兵可判定且互不相同，被拒前后
// 快照一致，引擎之后仍可用（钉住不变量 4）。
func TestRejectedOperationsLeaveNoTrace(t *testing.T) {
	e := NewEngine()
	if err := e.Upsert(1, "k", "a", 11); err != nil {
		t.Fatal(err)
	}
	before := snap(e)
	cases := []struct{ err, want error }{
		{e.Delete(404), ErrRowNotFound},
		{e.Upsert(0, "k", "a", 1), ErrInvalidRowID},
		{e.Upsert(-3, "k", "a", 1), ErrInvalidRowID},
		{e.Upsert(1, "", "a", 1), ErrEmptyKey},
	}
	for i, c := range cases {
		if !errors.Is(c.err, c.want) || snap(e) != before {
			t.Fatalf("case %d: %v", i, c.err)
		}
	}
	if ErrRowNotFound.Error() == ErrEmptyKey.Error() ||
		ErrEmptyKey.Error() == ErrInvalidRowID.Error() {
		t.Fatal("sentinel errors must be mutually distinct")
	}
	if err := e.Upsert(9, "j", "z", 9); err != nil || e.Total() != 2 {
		t.Fatal("engine unusable after rejected ops")
	}
}
