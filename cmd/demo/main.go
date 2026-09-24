// Command demo 演示 ontology-323：按键哈希的一致采样。不读参数、不联网。
package main

import (
	"cmp"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"sync"

	"ontology/api"
	"ontology/khash"
)

func events(ks []string) []api.Event {
	ev := make([]api.Event, len(ks))
	for i := range ks {
		ev[i] = api.Event{Key: ks[i], V: int64(i)}
	}
	return ev
}
func shuf(ks []string, seed uint64) []string {
	p := append([]string(nil), ks...)
	rand.New(rand.NewPCG(seed, seed+1)).Shuffle(len(p), func(i, j int) { p[i], p[j] = p[j], p[i] })
	return p
}
func byBK(a, b string) int { // 朴素参照所需的 (bucket,key) 比较
	return cmp.Or(cmp.Compare(khash.Bucket(a), khash.Bucket(b)), cmp.Compare(a, b))
}
func diffNaive(ks []string, r1, r2 int) (add, rem []string) { // SetRate 的朴素参照
	for _, k := range ks {
		b1, b2 := khash.Sampled(k, r1), khash.Sampled(k, r2)
		if !b1 && b2 {
			add = append(add, k)
		} else if b1 && !b2 {
			rem = append(rem, k)
		}
	}
	slices.SortFunc(add, byBK)
	slices.SortFunc(rem, byBK)
	return add, rem
}
func main() {
	fails := 0
	check := func(n string, ok bool) {
		fmt.Println(map[bool]string{true: "OK  ", false: "FAIL "}[ok] + n)
		if !ok {
			fails++
		}
	}
	k8 := []string{"gnj", "dzv", "gnk", "kcm", "dzv", "qjy", "a6m", "dzv"}
	b8 := []int{2499, 0, 2500, 6005, 0, 2000, 5000, 0}
	ok := khash.Hash("ab") == 3105
	evs := make([]api.Event, len(k8))
	for i, k := range k8 { // 八事件桶与判定(r=2500 保留5)
		ok = ok && khash.Bucket(k) == b8[i] && khash.Sampled(k, 2500) == (b8[i] < 2500)
		evs[i] = api.Event{Key: k, V: int64(i)}
	}
	check("eight events buckets & keep=5", ok)
	check("edge 2500/2000/5000 exclusive", !khash.Sampled("gnk", 2500) && !khash.Sampled("qjy", 2000) && !khash.Sampled("a6m", 5000))
	a, _ := api.New(2500, 100) // 两次 SetRate 返回列表
	out, _ := a.Feed(evs)
	a1, r1, _ := a.SetRate(2000)
	a2, r2, _ := a.SetRate(5000)
	check("setrate (-,[qjy gnj]) ([qjy gnj gnk],-)", len(out) == 5 && slices.Equal(a1, []string(nil)) &&
		slices.Equal(r1, []string{"qjy", "gnj"}) && slices.Equal(a2, []string{"qjy", "gnj", "gnk"}) && slices.Equal(r2, []string(nil)))
	base := make([]string, 400) // 随机率序列：差集=朴素参照、方向单调
	for i := range base {
		base[i] = fmt.Sprintf("k-%05d", i)
	}
	c, _ := api.New(0, 500)
	c.Feed(events(base))
	rng := rand.New(rand.NewPCG(1, 2))
	rate, mono := 0, true
	for range 200 {
		nr := rng.IntN(10001)
		ad, rm, err := c.SetRate(nr)
		na, nrm := diffNaive(base, rate, nr)
		mono = err == nil && slices.Equal(ad, na) && slices.Equal(rm, nrm) &&
			((nr >= rate && len(rm) == 0) || (nr <= rate && len(ad) == 0))
		if !mono {
			break
		}
		rate = nr
	}
	check("random rates monotonic & naive", mono)
	x, _ := api.New(3333, 500) // 两独立实例喂不同随机排列，判定一致
	y, _ := api.New(3333, 500)
	x.Feed(events(shuf(base, 7)))
	y.Feed(events(shuf(base, 9)))
	agree := true
	for _, k := range base {
		agree = agree && x.Sampled(k) == y.Sampled(k) && x.Sampled(k) == khash.Sampled(k, 3333)
	}
	check("two instances agree regardless of order", agree)
	_, e1 := api.New(-1, 10) // 三类哨兵错误互不相同；被拒后状态不变、仍可用
	_, e2 := api.New(10, 0)
	b, _ := api.New(100, 2)
	b.Feed(events([]string{"p", "q"}))
	_, e3 := b.Feed([]api.Event{{Key: ""}})
	_, e4 := b.Feed(events([]string{"s", "t"}))
	_, e5 := b.Feed(events([]string{"p"}))
	check("3 sentinel errors & rejected op leaves no trace", e1 == api.ErrRateOutOfRange &&
		e2 == api.ErrRateOutOfRange && e3 == api.ErrEmptyKey && e4 == api.ErrTooManyKeys && e5 == nil &&
		b.Sampled("p") == khash.Sampled("p", 100))
	check("SelfCheck four invariants", a.SelfCheck() == nil)
	bigOK := true // 大 m 窄区间只动桶恰为边界的极少数键（计数亚线性由白盒测试钉）
	for _, m := range []int{100, 1000, 10000} {
		d, _ := api.New(5000, m)
		ks := make([]string, m)
		for i := range ks {
			ks[i] = fmt.Sprintf("m%d-%05d", m, i)
		}
		d.Feed(events(ks))
		want, _ := diffNaive(ks, 5000, 5001)
		ad, _, _ := d.SetRate(5001)
		_, rm, _ := d.SetRate(5000)
		bigOK = bigOK && slices.Equal(ad, want) && slices.Equal(rm, want) && len(want) <= 2
	}
	check("large-m narrow setrate tiny & naive", bigOK)
	const N = 16 // N goroutine 并发喂同一共享实例的不同随机排列，键集相同且等于朴素参照
	res := make([][]string, N)
	var wg sync.WaitGroup
	shared, _ := api.New(2500, 500)
	for g := range N {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			o, _ := shared.Feed(events(shuf(base, uint64(100+g))))
			r := make([]string, len(o))
			for i, e := range o {
				r[i] = e.Key
			}
			slices.SortFunc(r, byBK)
			res[g] = r
		}(g)
	}
	wg.Wait()
	nai, _ := diffNaive(base, 0, 2500)
	concOK := true
	for _, r := range res {
		concOK = concOK && slices.Equal(r, nai)
	}
	check("concurrent shuffled feeds identical & naive", concOK)
	if fails != 0 {
		os.Exit(1)
	}
}
