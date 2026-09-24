package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sync"

	"ontology/api"
	"ontology/hash"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s: %s\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}

// norm 去掉空槽（0），返回各桶按放入顺序的指纹。
func norm(bs [][]int) [][]int {
	out := make([][]int, len(bs))
	for i, b := range bs {
		out[i] = slices.DeleteFunc(slices.Clone(b), func(v int) bool { return v == 0 })
	}
	return out
}

// naive 朴素扫描 i1/i2 两桶找指纹，用于核验 Lookup 只依赖两个候选桶。
func naive(bs [][]int, nb int, x int64) bool {
	fp := hash.Fingerprint(x)
	return slices.Contains(bs[hash.I1(x, nb)], fp) || slices.Contains(bs[hash.I2(x, nb)], fp)
}

func main() {
	// 第三节十步逐步核验（含第 7 步踢出 f3:b2->b3、第 8/10 步 Lookup(2)、第 9 步 Delete(9)）
	cf, _ := api.New(4, 2, 4)
	steps := []struct {
		op   string
		x    int64
		want [][]int
	}{
		{"i", 1, [][]int{{}, {2}, {}, {}}},
		{"i", 5, [][]int{{}, {2, 6}, {}, {}}},
		{"i", 9, [][]int{{3}, {2, 6}, {}, {}}},
		{"i", 2, [][]int{{3}, {2, 6}, {3}, {}}},
		{"i", 6, [][]int{{3}, {2, 6}, {3, 7}, {}}},
		{"i", 10, [][]int{{3, 4}, {2, 6}, {3, 7}, {}}},
		{"i", 14, [][]int{{3, 4}, {2, 6}, {7, 1}, {3}}},
		{"l", 2, [][]int{{3, 4}, {2, 6}, {7, 1}, {3}}},
		{"d", 9, [][]int{{4}, {2, 6}, {7, 1}, {3}}},
		{"l", 2, [][]int{{4}, {2, 6}, {7, 1}, {3}}},
	}
	ok := true
	for _, s := range steps {
		if s.op == "i" {
			ok = ok && cf.Insert(s.x) == nil
		} else if s.op == "d" {
			ok = ok && cf.Delete(s.x) == nil
		} else {
			ok = ok && cf.Lookup(s.x)
		}
		ok = ok && reflect.DeepEqual(norm(cf.Buckets()), s.want)
	}
	check("十步桶状态/踢出f3:b2->b3/Lookup(2)=true/Delete(9)", ok)

	// 无假阴性 + 指纹守恒：插入 0..199，删偶数，奇数必须全部 true
	bf, _ := api.New(128, 4, 8)
	ok = true
	for x := int64(0); x < 200 && ok; x++ {
		ok = bf.Insert(x) == nil && bf.Lookup(x)
	}
	for x := int64(0); x < 200 && ok; x += 2 {
		ok = bf.Delete(x) == nil
	}
	for x := int64(1); x < 200 && ok; x += 2 {
		ok = bf.Lookup(x)
	}
	check("无假阴性(插入过未删除的键必 true)", ok)
	check("指纹守恒(净指纹数=200-100=100)", bf.Count() == 100)
	// 四类可判定错误互不相同
	_, e1 := api.New(3, 2, 1)
	e2, e3 := cf.Insert(-1), cf.Delete(12345)
	sf, _ := api.New(4, 1, 1)
	for _, x := range []int64{1, 5, 2, 4} {
		sf.Insert(x)
	}
	before := sf.Buckets()
	e4 := sf.Insert(8)
	ok = errors.Is(e1, api.ErrInvalidParams) && errors.Is(e2, api.ErrNegativeKey) &&
		errors.Is(e3, api.ErrNotInserted) && errors.Is(e4, api.ErrFull) &&
		!errors.Is(e1, e2) && !errors.Is(e1, e3) && !errors.Is(e1, e4) &&
		!errors.Is(e2, e3) && !errors.Is(e2, e4) && !errors.Is(e3, e4)
	check("四类可判定错误(参数/负键/满/删未插入)互不相同", ok)

	// 过滤器满回滚后状态不变，且仍可继续正常使用
	ok = reflect.DeepEqual(before, sf.Buckets()) && sf.Count() == 4 &&
		sf.Delete(4) == nil && sf.Insert(8) == nil && sf.Lookup(8)
	check("过滤器满回滚后状态不变且可继续用", ok)
	// api 自检：内置操作序列核验四条不变量
	check("api.SelfCheck 四条不变量", api.SelfCheck() == nil)
	// 大桶数下 Lookup 只依赖两个候选桶（与两桶朴素扫描逐键一致）
	ok = true
	for _, nb := range []int{128, 512, 2048, 8192} {
		g, _ := api.New(nb, 4, 8)
		for x := int64(0); x < 100; x++ {
			g.Insert(x)
		}
		bs := g.Buckets()
		for x := int64(0); x < 300 && ok; x++ {
			ok = g.Lookup(x) == naive(bs, nb, x)
		}
	}
	check("大桶数(128..8192)下定位恒为 2 个候选桶", ok)
	// 并发 Lookup：8 个 goroutine 查同一批键，结果逐键相同
	pf, _ := api.New(256, 4, 8)
	for x := int64(0); x < 300; x++ {
		pf.Insert(x)
	}
	const G, N = 8, 600
	want := make([]bool, N)
	for x := 0; x < N; x++ {
		want[x] = pf.Lookup(int64(x))
	}
	got := make([][]bool, G)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			got[g] = make([]bool, N)
			for x := 0; x < N; x++ {
				got[g][x] = pf.Lookup(int64(x))
			}
		}(g)
	}
	wg.Wait()
	ok = true
	for g := 0; g < G && ok; g++ {
		ok = reflect.DeepEqual(got[g], want)
	}
	check("并发 Lookup 结果逐键一致", ok)
	if failed {
		os.Exit(1)
	}
}
