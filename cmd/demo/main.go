package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"unsafe"

	"ontology/api"
	"ontology/dp"
	"ontology/query"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

// fullTable 用完整 O(n·m) 表计算最长公共子串，用于对照滚动行结果。
func fullTable(a, b string) (best, start int) {
	d := make([][]int, len(a)+1)
	for i := range d {
		d[i] = make([]int, len(b)+1)
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				d[i][j] = d[i-1][j-1] + 1
			}
			if d[i][j] > best {
				best, start = d[i][j], i-d[i][j]
			}
		}
	}
	return best, start
}

// naive 枚举所有起点对逐字符扩展，取最大长度与最小起始下标。
func naive(a, b string) (best, start int) {
	for i := 0; i < len(a); i++ {
		for j := 0; j < len(b); j++ {
			k := 0
			for i+k < len(a) && j+k < len(b) && a[i+k] == b[j+k] {
				k++
			}
			if k > best {
				best, start = k, i
			}
		}
	}
	return best, start
}

// cellsOf 读取 dp.Core 的非导出计数器（不经由任何导出接口）。
func cellsOf(c *dp.Core) int {
	v := reflect.ValueOf(c).Elem().FieldByName("cells")
	return int(reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().Int())
}

var pairs = [][2]string{
	{"banana", "ananas"}, {"abcx", "abc"}, {"abcde", "abfce"}, {"aabbaabb", "bbaabbaa"},
	{"xyz", "abc"}, {"aaaa", "aa"}, {"mississippi", "issip"}, {"abracadabra", "cadabra"},
}

func main() {
	c := dp.New("banana")
	l, s := c.Longest("ananas")
	check("dp banana/ananas len=5 start=1", l == 5 && s == 1)
	ok := true
	for _, p := range pairs {
		gl, gs := dp.New(p[0]).Longest(p[1])
		fl, fs := fullTable(p[0], p[1])
		ok = ok && gl == fl && gs == fs
	}
	check("dp rolling == full table", ok)
	r := query.Exec("banana", dp.New("banana"), "ananas")
	check("query text=anana len=5 start=1", r.Text == "anana" && r.Length == 5 && r.Start == 1)

	k, _ := api.New("banana")
	l1, s1, e1 := k.Query("ananas")
	sub, e2 := k.Substring("ananas")
	k2, _ := api.New("abcx")
	l2, _, _ := k2.Query("abc")
	k3, _ := api.New("abcde")
	l3, _, _ := k3.Query("abfce")
	check("api known pairs 5/1 anana,3,2", e1 == nil && e2 == nil && l1 == 5 && s1 == 1 &&
		sub == "anana" && l2 == 3 && l3 == 2)

	ok = true
	for _, p := range pairs {
		kk, _ := api.New(p[0])
		gl, gs, err := kk.Query(p[1])
		nl, ns := naive(p[0], p[1])
		ok = ok && err == nil && gl == nl && gs == ns
	}
	check("api == naive", ok)

	_, eRef := api.New("")
	_, _, eQ := k.Query("")
	_, _, eL := k.Query(strings.Repeat("x", 1<<20))
	check("api 3 errors distinct", errors.Is(eRef, api.ErrEmptyRef) && errors.Is(eQ, api.ErrEmptyQuery) &&
		errors.Is(eL, api.ErrTooLong) && eRef != eQ && eQ != eL && eRef != eL)

	bl, bs, _ := k.Query("ananas")
	_, _, _ = k.Query("")
	_, _, _ = k.Query(strings.Repeat("x", 1<<20))
	al, as, _ := k.Query("ananas")
	check("api state intact after reject", bl == al && bs == as)
	ok = true
	big := dp.New(strings.Repeat("a", 100))
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		big.Longest(strings.Repeat("b", m))
		ok = ok && cellsOf(big) == min(100, m)+1
	}
	check("dp cells constant in m", ok)

	kc, _ := api.New("mississippi-abracadabra-banana")
	qs := []string{"issi", "cadabra", "anana", "xyz", "miss", "abra", "nab", "sip"}
	type res struct{ l, s int }
	want, got := make([]res, len(qs)), make([]res, len(qs))
	for i, b := range qs {
		want[i].l, want[i].s, _ = kc.Query(b)
	}
	var wg sync.WaitGroup
	for i, b := range qs {
		wg.Add(1)
		go func(i int, b string) {
			defer wg.Done()
			got[i].l, got[i].s, _ = kc.Query(b)
		}(i, b)
	}
	wg.Wait()
	check("api concurrent == serial", slices.Equal(want, got))

	sc, _ := api.New("selfcheck")
	check("api SelfCheck", sc.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
