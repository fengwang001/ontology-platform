package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/iter"
	"ontology/key"
	"ontology/mset"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func build(order []int) *mset.Mset {
	m := mset.New(mset.Options{})
	for _, v := range order {
		if err := m.Insert(key.Key(v)); err != nil {
			panic(err)
		}
	}
	return m
}

func main() {
	a := build([]int{3, 1, 4, 1, 5, 9, 2, 6, 5, 3, 5})
	b := build([]int{9, 6, 5, 5, 5, 4, 3, 3, 2, 1, 1})
	c := build([]int{1, 1, 2, 3, 3, 4, 5, 5, 5, 6, 9})
	check("三种插入顺序结构完全相同", a.Equal(b) && b.Equal(c))
	check("跨度与有序性自检通过", a.Check() == nil)

	inv := true
	for i := 0; i < a.Size(); i++ {
		k, err := a.At(i)
		if err != nil {
			inv = false
			break
		}
		idx, found := a.RankOf(k)
		back, err2 := a.At(idx)
		if !found || err2 != nil || idx > i || back != k {
			inv = false
		}
	}
	check("At 与 RankOf 互逆", inv)

	dup := build(nil)
	for _, v := range []int{7, 7, 7} {
		dup.Insert(key.Key(v))
	}
	idx, found := dup.RankOf(7)
	before := dup.Count(7)
	errDel := dup.Delete(7)
	check("重复元素 RankOf/Count/Delete 自洽",
		found && idx == 0 && before == 3 && errDel == nil && dup.Count(7) == 2)
	check("删除后跨度仍精确", dup.Check() == nil)

	lo, _ := a.RankOf(3)
	hi, _ := a.RankOf(6)
	rng, _ := a.Range(3, 6)
	check("Range 计数与 RankOf 差值一致", rng == hi-lo+a.Count(6))

	it := a.Iterate()
	_, ok0, _ := it.Next()
	a.Delete(3)
	_, _, errIt := it.Next()
	check("迭代期间写入立即失效", ok0 && errors.Is(errIt, iter.ErrInvalidated))

	_, errNeg := a.At(-1)
	_, errBig := a.At(a.Size() + 10)
	_, errRng := a.Range(9, 1)
	errMissing := a.Delete(1 << 40)
	limited := mset.New(mset.Options{MaxElements: 2, MaxLevel: 8})
	e1, e2, e3 := limited.Insert(1), limited.Insert(2), limited.Insert(3)
	check("四类可判定错误且超限不污染结构",
		errors.Is(errNeg, mset.ErrOutOfRange) && errors.Is(errBig, mset.ErrOutOfRange) &&
			errors.Is(errRng, mset.ErrBadRange) && errors.Is(errMissing, mset.ErrNotFound) &&
			e1 == nil && e2 == nil && errors.Is(e3, mset.ErrTooMany) && limited.Size() == 2)

	var wg sync.WaitGroup
	res := make([][]key.Key, 8)
	for g := range res {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < b.Size(); i++ {
				k, _ := b.At(i)
				r, _ := b.RankOf(k)
				n, _ := b.Range(k, k)
				res[g] = append(res[g], k, key.Key(r), key.Key(n))
			}
		}(g)
	}
	wg.Wait()
	same := true
	for g := 1; g < len(res); g++ {
		if len(res[g]) != len(res[0]) {
			same = false
		}
		for i := range res[0] {
			if res[g][i] != res[0][i] {
				same = false
			}
		}
	}
	check("并发只读结果逐位相同", same)

	scale := func(n int) (int64, int64) {
		m := build(nil)
		for v := 0; v < n; v++ {
			m.Insert(key.Key(v))
		}
		m.At(n / 2)
		va := m.LastVisited()
		m.RankOf(key.Key(n / 2))
		return va, m.LastVisited()
	}
	v1a, v1r := scale(1000)
	v2a, v2r := scale(100000)
	check(fmt.Sprintf("访问节点数不随规模线性增长 (1k:%d/%d 100k:%d/%d)", v1a, v1r, v2a, v2r),
		v1a <= 80 && v1r <= 80 && v2a <= 80 && v2r <= 80)

	if failed {
		panic("demo failed")
	}
}
