package main

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/join"
	"ontology/nkey"
)

func ok(cond bool, msg string) {
	if cond {
		fmt.Println("OK", msg)
	} else {
		fmt.Println("FAIL", msg)
	}
}

func sp(s string) *string { return &s }

func main() {
	ok(!nkey.Matches(nil, nil) && !nkey.Matches(nil, sp("k")),
		"NULL 不匹配任何键（含 NULL 自身）")
	ok(nkey.Matches(sp(""), sp("")) && !nkey.Matches(sp(""), nil),
		`空串 "" 与 NULL 是两个不同的键`)

	j := join.New()
	k, q := "k", ""
	sd := []join.Side{join.R, join.L, join.L, join.R, join.R, join.L, join.R, join.L}
	ks := []*string{&k, &k, nil, nil, &k, &q, &q, &k}
	vs := []int64{100, 1, 2, 200, 101, 3, 300, 4}
	want := []int{0, 1, 1, 1, 2, 2, 3, 5}
	eight := true
	for i := range want {
		j.Feed(sd[i], ks[i], vs[i])
		if len(j.Snapshot()) != want[i] {
			eight = false
		}
	}
	ok(eight && want[3] == want[2] && want[5] == want[4] && want[7]-want[6] == 2,
		"八步累计 0,1,1,1,2,2,3,5；第4/6步0条、第8步2条")

	g := join.New()
	g.Feed(join.R, sp("k"), 1)
	g.Feed(join.R, sp("k"), 2)
	g.Feed(join.L, sp("k"), 9)
	o := g.Snapshot()
	ok(len(o) == 2 && o[0].RVal == 1 && o[1].RVal == 2,
		"同 Key 多条缓冲行两两配对、按插入序输出")

	a, _ := api.New(3)
	long := "abcd"
	three := errors.Is(a.Feed(join.Side(9), sp("x"), 1), api.ErrInvalidSide) &&
		errors.Is(a.Feed(api.L, &long, 1), api.ErrKeyTooLong)
	_, bad := api.New(0)
	distinct := api.ErrInvalidSide != api.ErrInvalidMaxKeyLen &&
		api.ErrInvalidSide != api.ErrKeyTooLong && api.ErrInvalidMaxKeyLen != api.ErrKeyTooLong
	ok(three && errors.Is(bad, api.ErrInvalidMaxKeyLen) && distinct,
		"三类哨兵错误可判定且互不相同")

	before := len(a.Outputs())
	_ = a.Feed(join.Side(2), nil, 5)
	_ = a.Feed(api.R, &long, 5)
	after := len(a.Outputs())
	_ = a.Feed(api.L, sp("abc"), 5)
	_ = a.Feed(api.R, sp("abc"), 5)
	ok(before == after && after == 0 && len(a.Outputs()) == 1,
		"被拒操作不留痕，实例仍可继续正常使用")

	ok(j.SelfCheck() == nil,
		"大 m(100/1000/10000) 下探测数恒为命中数，不随 m 线性增长")

	var wg sync.WaitGroup
	res := make([][]api.Output, 16)
	for n := range res {
		wg.Add(1)
		go func(n int) { defer wg.Done(); res[n] = a.Outputs() }(n)
	}
	wg.Wait()
	same := true
	for n := 1; n < len(res); n++ {
		if !reflect.DeepEqual(res[0], res[n]) {
			same = false
		}
	}
	ok(same, "16 个 goroutine 并发只读，Outputs 逐字段相同")
}
