// 演示：时间旅行 as-of 查询。逐条打印 OK/FAIL，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/chain"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
		failed = true
	}
}

func main() {
	d := api.New()
	K := "demo"

	// 第三节八步：逐步执行并核验版本链（经 AsOf 探测）与 as-of 结果。
	d.Write(K, 10, "a")
	s1 := asOfAll(d, K, []int64{9, 10}) == ",a"
	d.Write(K, 30, "b")
	s2 := asOfAll(d, K, []int64{10, 29, 30}) == "a,a,b"
	d.Write(K, 20, "c")
	s3 := asOfAll(d, K, []int64{10, 19, 20, 29, 30}) == "a,a,c,c,b"
	v4, ok4 := d.AsOf(K, 15)
	v5, ok5 := d.AsOf(K, 30)
	d.Delete(K, 40)
	v7, ok7 := d.AsOf(K, 25)
	_, ok8 := d.AsOf(K, 40)
	check("步1 [10:a] | 步2 [10:a,30:b] | 步3 [10:a,20:c,30:b] | 步4 AsOf15=a（第4步判定）",
		s1 && s2 && s3 && ok4 && v4 == "a")
	check("步5 AsOf30=b | 步6 [10:a,20:c,30:b,40:删] | 步7 AsOf25=c | 步8 AsOf40=不存在（第8步判定）",
		ok5 && v5 == "b" && ok7 && v7 == "c" && !ok8)

	// 越界 T（小于链上最小 ts）返回不存在。
	_, ok := d.AsOf(K, 5)
	check("越界 T=5 返回不存在", !ok)

	// tombstone 可见性：删除后、再写之前都不可见。
	_, ok = d.AsOf(K, 45)
	check("tombstone 可见性 AsOf(45)=不存在", !ok)

	// 版本链不可变：新插入不改既有版本（旧时刻查询结果不变）。
	vOld, okOld := d.AsOf(K, 10)
	d.Write(K, 12, "z")
	d.Delete(K, 14)
	vNow, okNow := d.AsOf(K, 10)
	check("版本链不可变 AsOf(10) 始终=a", okOld && okNow && vOld == "a" && vNow == "a")

	// 三类互不相同的可判定错误。
	e1 := d.Write("", 1, "v")
	e2 := d.Write(K, -1, "v")
	e3 := d.Write(K, 1, "")
	e4 := d.Delete("", 1)
	e5 := d.Delete(K, -1)
	check("三类可判定错误 空key/负ts/空值",
		errors.Is(e1, api.ErrEmptyKey) && errors.Is(e4, api.ErrEmptyKey) &&
			errors.Is(e2, api.ErrBadTime) && errors.Is(e5, api.ErrBadTime) &&
			errors.Is(e3, api.ErrEmptyValue) &&
			api.ErrEmptyKey != api.ErrBadTime && api.ErrBadTime != api.ErrEmptyValue)

	// 被拒后状态不变且可继续使用。
	before := d.ViewAsOf(100)
	d.Write("", 1, "x")
	d.Write(K, -1, "x")
	d.Write(K, 1, "")
	d.Delete(K, -1)
	after := d.ViewAsOf(100)
	_, stillOK := d.AsOf(K, 30)
	check("被拒后状态不变且可继续用", viewEq(before, after) && stillOK)

	// 大 m 下比较个数不随 m 增长（chain 内部计数器自检，不读数值）。
	check("大 m 比较个数不随 m 增长", chain.SelfCheckCompares() == nil)

	// 并发 as-of 结果一致。
	check("并发 as-of 结果一致", concurrentSame(d, K, 30, 32))

	// 内置自检（四条不变量）。
	check("SelfCheck 四条不变量", d.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}

// asOfAll 依次查询各 T，存在的值用逗号拼接（不存在为空段），用于探测链形态。
func asOfAll(d *api.DB, key string, Ts []int64) string {
	out := ""
	for i, T := range Ts {
		if i > 0 {
			out += ","
		}
		if v, ok := d.AsOf(key, T); ok {
			out += v
		}
	}
	return out
}

func viewEq(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func concurrentSame(d *api.DB, key string, T int64, n int) bool {
	type res struct {
		s string
		b bool
	}
	results := make([]res, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for g := 0; g < n; g++ {
		g := g
		go func() {
			defer wg.Done()
			v, ok := d.AsOf(key, T)
			results[g] = res{v, ok}
		}()
	}
	wg.Wait()
	for _, r := range results[1:] {
		if r != results[0] {
			return false
		}
	}
	return results[0].b
}
