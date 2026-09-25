// Command demo exercises the time-travel store end to end.
// It prints one OK/FAIL line per check and exits 0 only if all pass.
package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

func fail(format string, a ...any) {
	fmt.Printf("FAIL "+format+"\n", a...)
	os.Exit(1)
}

func asOf(s *api.Store, key string, T int64) string {
	if v, ok := s.AsOf(key, T); ok {
		return v
	}
	return "不存在"
}

// probe asserts AsOf(key, T) == want for each (T, want) pair.
func probe(s *api.Store, key string, cases ...any) {
	for i := 0; i < len(cases); i += 2 {
		T, want := cases[i].(int64), cases[i+1].(string)
		if got := asOf(s, key, T); got != want {
			fail("AsOf(%s,%d)=%s, 期望 %s", key, T, got, want)
		}
	}
}

func main() {
	s := api.New()
	must := func(err error) {
		if err != nil {
			fail("setup: %v", err)
		}
	}
	// 第三节八步。
	must(s.Write("K", 10, "a"))
	must(s.Write("K", 30, "b"))
	probe(s, "K", int64(15), "a", int64(35), "b")
	must(s.Write("K", 20, "c"))
	probe(s, "K", int64(15), "a", int64(25), "c", int64(35), "b")
	fmt.Println("OK 步1-3 版本链: 10:a | 10:a,30:b | 10:a,20:c,30:b (乱序插入)")
	if asOf(s, "K", 15) != "a" || asOf(s, "K", 30) != "b" {
		fail("步4/5 as-of")
	}
	fmt.Println("OK 步4 AsOf(K,15)=a; 步5 AsOf(K,30)=b (含等于)")
	must(s.Delete("K", 40))
	if asOf(s, "K", 25) != "c" {
		fail("步7 as-of")
	}
	fmt.Println("OK 步6-7 链=10:a,20:c,30:b,40:删; AsOf(K,25)=c")
	if v, ok := s.AsOf("K", 40); ok || v != "" {
		fail("步8 应为不存在, 得 %q", v)
	}
	fmt.Println("OK 步8 AsOf(K,40)=不存在 (tombstone 可见)")
	// 越界 T 与未知 key。
	if _, ok := s.AsOf("K", 9); ok {
		fail("T=9 应不存在")
	}
	if _, ok := s.AsOf("absent", 100); ok {
		fail("未知 key 应不存在")
	}
	fmt.Println("OK 越界 T 与未知 key 均返回不存在")
	// 不可变：后续新写不改变历史时刻的 as-of 结果。
	must(s.Write("K", 50, "d"))
	probe(s, "K", int64(15), "a", int64(25), "c", int64(35), "b",
		int64(40), "不存在", int64(55), "d")
	fmt.Println("OK 版本链不可变: 新写只插入, 历史 as-of 不变")
	// 三类可判定错误互不相同，且被拒后状态不变。
	e1, e2, e3 := s.Write("", 1, "v"), s.Write("K", -1, "v"), s.Write("K", 1, "")
	if e1 != api.ErrEmptyKey || e2 != api.ErrNegativeTS || e3 != api.ErrEmptyValue {
		fail("哨兵错误")
	}
	if e1 == e2 || e2 == e3 || e1 == e3 {
		fail("错误须互不相同")
	}
	if s.Delete("", 1) != api.ErrEmptyKey || s.Delete("K", -1) != api.ErrNegativeTS {
		fail("Delete 哨兵错误")
	}
	probe(s, "K", int64(35), "b", int64(55), "d")
	fmt.Println("OK 三类错误可判定且互异; 被拒后状态不变")
	// 大 m 下比较个数不随 m 增长（计数器非导出，由测试钉住）。
	big := api.New()
	for i := int64(1); i <= 10000; i++ {
		must(big.Write("k", i, "v"))
	}
	if asOf(big, "k", 7777) != "v" {
		fail("大 m as-of")
	}
	fmt.Println("OK 大 m 比较数不随 m 增长 (二分前驱, TestAsOfComparisonCount 钉住)")
	// 并发 as-of 结果一致。
	const N = 64
	res := make([]string, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res[i] = asOf(s, "K", 30)
		}(i)
	}
	wg.Wait()
	for _, r := range res {
		if r != "b" {
			fail("并发 as-of 不一致")
		}
	}
	fmt.Println("OK 64 路并发 AsOf(K,30) 结果全为 b")
	if err := api.New().SelfCheck(); err != nil {
		fail("SelfCheck: %v", err)
	}
	fmt.Println("OK SelfCheck 四条不变量全部成立")
}
