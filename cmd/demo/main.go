package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/join"
	"ontology/rstore"
)

var failed bool

func check(name string, ok bool) {
	s := "OK"
	if !ok {
		s = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", s, name)
}

func pi(v int64) *int64                         { return &v }
func ins(k string, lv int64, rv *int64) join.Op { return join.Op{Insert: true, K: k, LV: lv, RV: rv} }
func del(k string, lv int64, rv *int64) join.Op { return join.Op{K: k, LV: lv, RV: rv} }

func main() {
	// rstore：保存/查询/删除/快照
	s := rstore.New()
	s.Put("a", 1)
	v1, ok1 := s.Get("a")
	s.Put("a", 2)
	v2, _ := s.Get("a")
	check("rstore put/get/del", ok1 && v1 == 1 && v2 == 2 && s.Del("a") && !s.Del("a") && len(s.Snapshot()) == 0)

	// 第三节八步序列：逐步核对变更日志（含补发/更新/无输出）与最终宽表
	j := join.New()
	calls := []struct {
		op   string
		k    string
		v    int64
		want []join.Op
	}{
		{"PutL", "k1", 10, []join.Op{ins("k1", 10, nil)}},
		{"PutL", "k2", 20, []join.Op{ins("k2", 20, nil)}},
		{"PutR", "k2", 200, []join.Op{del("k2", 20, nil), ins("k2", 20, pi(200))}}, // 补发
		{"PutR", "k1", 100, []join.Op{del("k1", 10, nil), ins("k1", 10, pi(100))}}, // 补发
		{"PutR", "k3", 300, nil},                                                       // 无左：仅存不输出
		{"PutL", "k3", 30, []join.Op{ins("k3", 30, pi(300))}},                          // 右晚到先存后复用
		{"PutR", "k2", 250, []join.Op{del("k2", 20, pi(200)), ins("k2", 20, pi(250))}}, // 更新
	}
	ok8 := true
	for _, c := range calls {
		var got []join.Op
		var err error
		if c.op == "PutL" {
			got, err = j.PutL(c.k, c.v)
		} else {
			got, err = j.PutR(c.k, c.v)
		}
		if err != nil || !reflect.DeepEqual(got, c.want) {
			ok8 = false
		}
	}
	d1, err := j.DelL("k1")
	wantView := []join.Row{{K: "k2", LV: 20, RV: pi(250)}, {K: "k3", LV: 30, RV: pi(300)}}
	check("eight-step logs+view", ok8 && err == nil &&
		reflect.DeepEqual(d1, []join.Op{del("k1", 10, pi(100))}) && reflect.DeepEqual(j.View(), wantView))

	// 左删除后右记录留存：再 PutL(k1,15) 复用删除前的右值 100
	g, err := j.PutL("k1", 15)
	check("right retained after DelL", err == nil && reflect.DeepEqual(g, []join.Op{ins("k1", 15, pi(100))}))

	// api 自检：批量重算一致、日志前缀自洽、右记录留存、失败不留痕
	check("api SelfCheck (invariants 1-4)", api.New().SelfCheck() == nil)

	// 三类哨兵错误可判定且互不相同；被拒后状态不变、仍可正常使用
	a := api.New()
	_, e1 := a.PutL("", 1)
	_, _ = a.PutL("x", 1)
	_, e2 := a.DelL("ghost")
	_, e3 := a.DelR("x") // x 有左但 RV 缺席
	_, err = a.PutL("y", 2)
	check("sentinel errors distinct, state intact",
		e1 != e2 && e2 != e3 && e1 != e3 &&
			errors.Is(e1, api.ErrEmptyKey) && errors.Is(e2, api.ErrNoLeft) && errors.Is(e3, api.ErrNoRight) &&
			err == nil && len(a.View()) == 2)

	// 大 m 补发结果正确；检查个数的对数上界由 join.TestChecksLogarithmic 钉住
	b := api.New()
	for i := 0; i < 10000; i++ {
		_, _ = b.PutL(fmt.Sprintf("k%05d", i), int64(i))
	}
	ops, err := b.PutR("k05000", 7)
	check("large-m backfill (log bound in join test)", err == nil &&
		reflect.DeepEqual(ops, []api.Op{del("k05000", 5000, nil), ins("k05000", 5000, pi(7))}))

	// 并发：8 个 goroutine 只读 View 结果一致，同时另一 goroutine 做合法写
	c := api.New()
	for i := 0; i < 200; i++ {
		_, _ = c.PutL(fmt.Sprintf("c%04d", i), int64(i))
	}
	_, _ = c.PutL("w", 1)
	want := c.View()
	bad := make(chan struct{}, 1)
	var wg sync.WaitGroup
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 50; n++ {
				if !reflect.DeepEqual(c.View(), want) {
					select {
					case bad <- struct{}{}:
					default:
					}
				}
			}
		}()
	}
	wg.Add(1)
	go func() { // 合法写：同值重写与无左右记录，均不改变宽表
		defer wg.Done()
		for n := 0; n < 200; n++ {
			_, _ = c.PutL("w", 1)
			_, _ = c.PutR("zz-orphan", 7)
		}
	}()
	wg.Wait()
	check("concurrent readers consistent", len(bad) == 0)

	if failed {
		os.Exit(1)
	}
}
