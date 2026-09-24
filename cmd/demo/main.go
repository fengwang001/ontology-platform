package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/idx"
	"ontology/segment"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

// 第三节的八条记录（位点/字节数）与期望物理位置。
var eight = [][2]int64{
	{1000, 60}, {1001, 50}, {1003, 40}, {1004, 60},
	{1007, 30}, {1008, 80}, {1010, 20}, {1012, 50},
}
var wantPos = []int64{0, 60, 110, 150, 210, 240, 320, 340}

func naive(recs [][2]int64, t int64) (int64, int64, error) {
	for _, r := range recs {
		if r[0] >= t {
			return r[0], r[1], nil
		}
	}
	return 0, 0, api.ErrNotFound
}
func main() {
	var ix idx.Index
	ix.Append(3, 110)
	ix.Append(7, 210)
	ix.Append(10, 320)
	e, ok, _ := ix.Floor(4)
	_, miss, _ := ix.Floor(2)
	check("idx floor 二分", ok && e.Rel == 3 && e.Pos == 110 && !miss)
	seg, err := segment.New(1000, 100)
	ok = err == nil
	for i, r := range eight {
		pos, err := seg.Append(r[0], r[1])
		ok = ok && err == nil && pos == wantPos[i]
	}
	want := []idx.Entry{{Rel: 3, Pos: 110}, {Rel: 7, Pos: 210}, {Rel: 10, Pos: 320}}
	check("segment 八步物理位置+完整索引 [(3,110),(7,210),(10,320)]",
		ok && fmt.Sprint(seg.Entries()) == fmt.Sprint(want))

	a, _ := api.New(1000, 100)
	for _, r := range eight {
		a.Append(r[0], r[1])
	}
	o1, p1, e1 := a.Lookup(1004)
	o2, p2, e2 := a.Lookup(1006)
	check("api Lookup(1004)=(1004,150)扫2条 Lookup(1006)=(1007,210)扫3条",
		e1 == nil && o1 == 1004 && p1 == 150 && e2 == nil && o2 == 1007 && p2 == 210)
	o3, p3, _ := a.Lookup(1003)
	o4, p4, _ := a.Lookup(1007)
	check("api >= 边界（floor 取等）", o3 == 1003 && p3 == 110 && o4 == 1007 && p4 == 210)
	_, err = a.Append(1000+2147483648, 10)
	_, _, err2 := a.Lookup(999)
	check("api 相对位点溢出与低于基位点被拒",
		errors.Is(err, api.ErrOverflow) && errors.Is(err2, api.ErrBelowBase))

	errs := []error{api.ErrInvalid, api.ErrOverflow, api.ErrBelowBase, api.ErrNotFound}
	ok = true
	for i := range errs {
		for j := range errs {
			ok = ok && (i == j) == errors.Is(errs[i], errs[j])
		}
	}
	_, _, err = a.Lookup(1013)
	check("api 四类可判定错误互不相同", ok && errors.Is(err, api.ErrNotFound))

	before := fmt.Sprint(a.Entries())
	a.Append(999, 10)
	a.Append(1012, 5)
	a.Append(1013, 0)
	pos, err := a.Append(1013, 40)
	check("api 被拒后状态不变且可继续用",
		before == fmt.Sprint(a.Entries()) && err == nil && pos == 390)

	rnd := rand.New(rand.NewSource(1))
	b, _ := api.New(0, 40)
	var recs [][2]int64
	off, ok := int64(0), true
	for i := 0; i < 500; i++ {
		pos, _ := b.Append(off, int64(1+rnd.Intn(30)))
		recs = append(recs, [2]int64{off, pos})
		off += int64(rnd.Intn(5))
	}
	for i := 0; i < 2000; i++ {
		t := rnd.Int63n(off + 1)
		o, p, err := b.Lookup(t)
		no, np, ne := naive(recs, t)
		ok = ok && o == no && p == np && errors.Is(err, ne)
	}
	check("api 随机目标与朴素参照一致（含大 m 规模）", ok)

	c, _ := api.New(0, 40)
	const m = 5000
	positions := make([]int64, m)
	var n atomic.Int64
	var good atomic.Bool
	good.Store(true)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < m; i++ {
			positions[i], _ = c.Append(int64(i), 10)
			n.Store(int64(i + 1))
		}
	}()
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for k := 0; k < 2000; k++ {
				if cnt := n.Load(); cnt > 0 {
					t := r.Int63n(cnt)
					o, p, err := c.Lookup(t)
					if err != nil || o != t || p != positions[t] {
						good.Store(false)
					}
				}
			}
		}(int64(g))
	}
	wg.Wait()
	check("api 大 m 并发查找结果正确（增长上界见 segment 测试）", good.Load())

	check("api SelfCheck 四条不变量", a.SelfCheck() == nil && b.SelfCheck() == nil && c.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
