package main

import (
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/entry"
	"ontology/ttl"
)

var failed bool

func check(name string, ok bool) {
	tag := "OK"
	if !ok {
		tag, failed = "FAIL", true
	}
	fmt.Println(tag, name)
}

// sevenStep 复现 NOTES.md 七步轨迹：每步结果与 View（未过期集合），最终视图为空。
func sevenStep() bool {
	s, _ := api.New(10)
	eq := func(now int64, want map[string]string) bool {
		got, _ := s.View(now)
		return reflect.DeepEqual(got, want)
	}
	_ = s.Set("k1", "v1", 5)
	ok := eq(5, map[string]string{"k1": "v1"})
	_ = s.Set("k2", "v2", 7)
	ok = ok && eq(7, map[string]string{"k1": "v1", "k2": "v2"})
	v, hit := s.Get("k1", 14)
	ok = ok && hit && v == "v1" && eq(14, map[string]string{"k1": "v1", "k2": "v2"})
	_ = s.Set("k3", "v3", 15)
	ok = ok && eq(15, map[string]string{"k2": "v2", "k3": "v3"}) // k1 已过期(15−5=10)
	_, hit = s.Get("k1", 15)
	ok = ok && !hit && eq(15, map[string]string{"k2": "v2", "k3": "v3"})
	n, _ := s.Sweep(20)
	ok = ok && n == 1 && eq(20, map[string]string{"k3": "v3"})
	_, hit = s.Get("k3", 25)
	return ok && !hit && eq(25, map[string]string{})
}

type rec struct {
	v  string
	ts int64
}

// simulate 用错误实现重跑七步：refresh=Get 刷新 Ts（乙），noopSweep=Sweep 空操作（丙）。
func simulate(refresh, noopSweep bool) map[string]rec {
	m := map[string]rec{}
	set := func(k, v string, now int64) { m[k] = rec{v, now} }
	get := func(k string, now int64) {
		if r, ok := m[k]; !ok || now-r.ts >= 10 {
			delete(m, k)
		} else if refresh {
			m[k] = rec{r.v, now}
		}
	}
	sweep := func(now int64) {
		for k, r := range m {
			if !noopSweep && now-r.ts >= 10 {
				delete(m, k)
			}
		}
	}
	set("k1", "v1", 5)
	set("k2", "v2", 7)
	get("k1", 14)
	set("k3", "v3", 15)
	get("k1", 15)
	sweep(20)
	get("k3", 25)
	return m
}

// concurrent 64 写 64 读并发，结束后 View 逐 Key 正确。
func concurrent() bool {
	s, _ := api.New(10)
	const n = 64
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(2 * n)
	for i := 0; i < n; i++ {
		go func(i int) { defer wg.Done(); <-start; _ = s.Set(fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i), 1000) }(i)
		go func() { defer wg.Done(); <-start; _, _ = s.Get("k0", 1000); _, _ = s.View(1000) }()
	}
	close(start)
	wg.Wait()
	v, _ := s.View(1000)
	for i := 0; i < n; i++ {
		if v[fmt.Sprintf("k%d", i)] != fmt.Sprintf("v%d", i) {
			return false
		}
	}
	return len(v) == n
}

func main() {
	s0 := ttl.New(10)
	_ = s0.Set("k", "v", 5)
	_, hit := s0.Get("k", 14)
	_, exp := s0.Get("k", 15)
	check("entry/ttl: 过期含等于、惰性删除、时钟单调", entry.Expired(15, 5, 10) &&
		!entry.Expired(14, 5, 10) && hit && !exp && s0.Set("z", "9", 14) == ttl.ErrBackwardClock)
	check("api: 七步轨迹每步保留 key 与最终视图为空", sevenStep())
	check("(甲) 严格大于第5步会错返回 v1", entry.Expired(15, 5, 10) && !(15-5 > 10))
	check("(乙) Get 刷新 Ts 后 k1 残留", simulate(true, false)["k1"].v == "v1")
	check("(丙) Sweep 空操作后 k2 残留", simulate(false, true)["k2"].v == "v2")
	self, _ := api.New(10)
	check("与朴素判定一致 + SelfCheck", self.SelfCheck() == nil)
	// 有界内存：过期 Get 立即删除，Sweep 后无残留。
	bs, _ := api.New(10)
	_ = bs.Set("old", "x", 0)
	_ = bs.Set("new", "y", 9)
	_, okOld := bs.Get("old", 10)
	nB, _ := bs.Sweep(19)
	vB, _ := bs.View(19)
	check("有界内存: 过期即删、Sweep 无残留", !okOld && nB == 1 && len(vB) == 0)
	// 时钟单调 + 三类哨兵错误互不相同 + 被拒后状态不变。
	fs, _ := api.New(10)
	_ = fs.Set("x", "1", 100)
	e1 := fs.Set("y", "2", 99)
	e2 := fs.Set("", "3", 101)
	_, e3 := fs.Sweep(50)
	vF, _ := fs.View(100)
	_, err0 := api.New(0)
	d := err0 == api.ErrBadTTL && api.ErrBadTTL != api.ErrBackwardClock &&
		api.ErrBackwardClock != api.ErrEmptyKey && api.ErrBadTTL != api.ErrEmptyKey
	check("时钟单调 + 三类错误互异 + 拒后状态不变", d && e1 == api.ErrBackwardClock &&
		e2 == api.ErrEmptyKey && e3 == api.ErrBackwardClock && len(vF) == 1 && vF["x"] == "1" &&
		fs.Set("z", "4", 101) == nil)
	// 大 m 全新鲜时 Sweep 删除 0 个；检查个数 O(1) 由 ttl 包内测试钉住。
	big, _ := api.New(10)
	for i := 0; i < 10000; i++ {
		_ = big.Set(fmt.Sprintf("k%d", i), "v", 1000)
	}
	nBig, _ := big.Sweep(1001)
	vBig, _ := big.View(1001)
	check("大 m 下 Sweep 检查个数不随 m 增长", nBig == 0 && len(vBig) == 10000)
	check("并发读写正确", concurrent())
	if failed {
		os.Exit(1)
	}
}
