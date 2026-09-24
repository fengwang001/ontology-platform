package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

func TestEightSteps(t *testing.T) { // 第三节八步分步表（不变量 2、3）
	d := api.New()
	ops := []api.Op{api.Put("a", 5), api.Put("b", 5), api.Put("c", 8), api.Put("a", 8), api.Del("b"), api.Put("d", 5)}
	eq5 := [][]string{{"a"}, {"a", "b"}, {"a", "b"}, {"b"}, nil, {"d"}}
	eq8 := [][]string{nil, nil, {"c"}, {"a", "c"}, {"a", "c"}, {"a", "c"}}
	for i, op := range ops {
		_ = d.Apply([]api.Op{op})
		g5, _ := d.Eq(5)
		g8, _ := d.Eq(8)
		if !slices.Equal(g5, eq5[i]) || !slices.Equal(g8, eq8[i]) {
			t.Fatalf("step %d: Eq(5)=%v Eq(8)=%v want %v/%v", i+1, g5, g8, eq5[i], eq8[i])
		}
	}
	got, _ := d.Range(5, 8) // 第 7、8 步
	if g8, _ := d.Eq(8); !slices.Equal(got, []string{"d"}) || !slices.Equal(g8, []string{"a", "c"}) {
		t.Fatalf("steps 7-8: Range(5,8)=%v Eq(8)=%v", got, g8)
	}
}
func TestUpdateAtomic(t *testing.T) { // 更新后旧组不可见、删除后任何组都不可见、同 F 无重复项
	d := api.New()
	_ = d.Apply([]api.Op{api.Put("a", 5), api.Put("a", 8), api.Put("a", 8), api.Del("a")})
	g5, _ := d.Eq(5)
	g8, _ := d.Eq(8)
	if len(g5)+len(g8) != 0 {
		t.Fatalf("Eq(5)=%v Eq(8)=%v, want both empty (no old-group/dup residue)", g5, g8)
	}
}
func TestRangeHalfOpen(t *testing.T) { // 左闭右开、lo>=hi 返回空、结果升序无重复
	d := api.New()
	_ = d.Apply([]api.Op{api.Put("a", 0), api.Put("b", 1), api.Put("c", 2), api.Put("d", 3)})
	los := []int64{0, 0, 2, 3}
	his := []int64{3, 4, 2, 1}
	wants := [][]string{{"a", "b", "c"}, {"a", "b", "c", "d"}, nil, nil}
	for i := range wants {
		if got, _ := d.Range(los[i], his[i]); !slices.Equal(got, wants[i]) {
			t.Errorf("Range(%d,%d)=%v want %v", los[i], his[i], got, wants[i])
		}
	}
}
func TestBatchConsistent(t *testing.T) { // 多种子随机序列后逐查询与批量重算一致（不变量 1）
	for _, seed := range []int64{1, 2, 3} {
		d, model := api.New(), map[string]int64{}
		r := rand.New(rand.NewSource(seed))
		for i := 0; i < 300; i++ {
			op := api.Put(string(rune('a'+r.Intn(8))), int64(r.Intn(6)))
			if r.Intn(3) == 0 {
				op = api.Del(op.PK)
			}
			_, ok := model[op.PK]
			if err := d.Apply([]api.Op{op}); err != nil {
				if op.Del && !ok && errors.Is(err, api.ErrNotFound) {
					continue
				}
				t.Fatalf("seed=%d apply %v: %v", seed, op, err)
			}
			if op.Del {
				delete(model, op.PK)
			} else {
				model[op.PK] = op.F
			}
		}
		for f := int64(-1); f < 6; f++ {
			var want, want3 []string
			for k, v := range model {
				if v == f {
					want = append(want, k)
				}
				if v >= f && v < f+3 {
					want3 = append(want3, k)
				}
			}
			slices.Sort(want)
			slices.Sort(want3)
			eqGot, _ := d.Eq(f)
			rgGot, _ := d.Range(f, f+3)
			if !slices.Equal(eqGot, want) || !slices.Equal(rgGot, want3) {
				t.Fatalf("seed=%d f=%d inconsistent with batch recompute", seed, f)
			}
		}
	}
}
func TestRejectNoTrace(t *testing.T) { // 三类错误可判定且互不相同，被拒后状态不变（不变量 4）
	d := api.New()
	_ = d.Apply([]api.Op{api.Put("a", 1)})
	before, _ := d.Range(-10, 10)
	e1 := d.Apply([]api.Op{api.Put("", 1)})
	e2 := d.Apply([]api.Op{api.Del("ghost")})
	e3 := d.Apply([]api.Op{api.Put("b", 2), api.Del("ghost")})
	ok := errors.Is(e1, api.ErrEmptyPK) && !errors.Is(e1, api.ErrBatch) &&
		errors.Is(e2, api.ErrNotFound) && !errors.Is(e2, api.ErrBatch) &&
		errors.Is(e3, api.ErrBatch) && errors.Is(e3, api.ErrNotFound)
	if !ok {
		t.Fatalf("errors not distinct: %v / %v / %v", e1, e2, e3)
	}
	if after, _ := d.Range(-10, 10); !slices.Equal(before, after) {
		t.Fatalf("rejected batches changed state: %v -> %v", before, after)
	}
	_ = d.Apply([]api.Op{api.Put("b", 2)}) // 被拒后仍可正常使用
}
func TestConcurrentReaders(t *testing.T) { // 并发只读（Eq/Range 交替）结果逐元素相同，另一 goroutine 循环 Apply
	d := api.New()
	want := make([][]string, 10)
	for i := 0; i < 1000; i++ {
		_ = d.Apply([]api.Op{api.Put(fmt.Sprintf("k%04d", i), int64(i%10))})
		want[i%10] = append(want[i%10], fmt.Sprintf("k%04d", i))
	}
	var wg sync.WaitGroup
	var bad atomic.Bool
	wg.Add(5)
	for g := 0; g < 4; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				f := int64(i % 10)
				got, _ := d.Range(f, f+1)
				if i%2 == 0 {
					got, _ = d.Eq(f)
				}
				if !slices.Equal(got, want[f]) {
					bad.Store(true)
				}
			}
		}()
	}
	go func() { // 合法写只触碰 F=10^6 的键，读者查询范围不可见
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = d.Apply([]api.Op{api.Put(fmt.Sprintf("w%03d", i), 1_000_000)})
		}
	}()
	wg.Wait()
	if bad.Load() {
		t.Fatal("concurrent readers diverged")
	}
}
