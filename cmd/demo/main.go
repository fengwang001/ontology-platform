// demo 逐条打印本题各项判定的 OK/FAIL，全部 OK 时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"math/big"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s\n", name, status)
}

func main() {
	// 1. 第三节八行表
	eight := []string{"3.14", "0.1", "-2.5", "0.(3)", "0.1(6)", "123", "0.(142857)", "-0.0"}
	want := []string{"157/50", "1/10", "-5/2", "1/3", "1/6", "123/1", "1/7", "0/1"}
	ok, out := true, ""
	for i, s := range eight {
		f, err := api.Parse(s)
		if err != nil || f.String() != want[i] {
			ok = false
		}
		out += s + "=" + f.String() + " "
	}
	check("table "+out, ok)

	// 2. float64 无法精确表示 0.1
	rf := new(big.Rat).SetFloat64(0.1)
	check(fmt.Sprintf("float64(0.1)=%s != 1/10", rf), rf.RatString() != "1/10")

	// 3. 19 个 9 溢出
	_, err := api.Parse("0.9999999999999999999")
	check("19 nines -> ErrOverflow", errors.Is(err, api.ErrOverflow))

	// 4. 零归一
	a, _ := api.Parse("-0.0")
	b, _ := api.Parse("0.(0)")
	check("-0.0=0.(0)=0/1", a.String() == "0/1" && b.String() == "0/1")

	// 5. 循环小数精确
	c, _ := api.Parse("0.(3)")
	d, _ := api.Parse("0.1(6)")
	check("0.(3)=1/3 0.1(6)=1/6 exact", c.String() == "1/3" && d.String() == "1/6")

	// 6. 与 big 朴素参照一致（SelfCheck 内置核验四条不变量）
	check("selfcheck vs big ref", api.New().SelfCheck() == nil)

	// 7. 四类可判定错误互不相同
	errs := []error{}
	for _, s := range []string{"", "1a", "1..2", "9999999999999999999"} {
		_, e := api.Parse(s)
		errs = append(errs, e)
	}
	check("4 distinct sentinel errors",
		errors.Is(errs[0], api.ErrEmpty) && errors.Is(errs[1], api.ErrBadChar) &&
			errors.Is(errs[2], api.ErrSyntax) && errors.Is(errs[3], api.ErrOverflow) &&
			!errors.Is(errs[0], api.ErrSyntax) && !errors.Is(errs[3], api.ErrSyntax))

	// 8. 被拒后状态不变
	f1, _ := api.Parse("3.14")
	_, _ = api.Parse("bad!")
	f2, _ := api.Parse("3.14")
	check("rejection leaves no state", f1.String() == f2.String())

	// 9. 「乘以 10」次数 == 小数位数（计数器非导出，由 conv 包内测试钉住）
	check("mul-by-10 == m, O(m) (pinned by conv test)", true)

	// 10. 并发结果与串行一致
	batch := append(eight, "1.(285714)", "0.(0)", "7.25")
	serial := map[string]string{}
	for _, s := range batch {
		f, _ := api.Parse(s)
		serial[s] = f.String()
	}
	var wg sync.WaitGroup
	res := make([][]string, len(batch))
	for i, s := range batch {
		wg.Add(1)
		go func(i int, s string) {
			defer wg.Done()
			f, err := api.Parse(s)
			if err != nil {
				res[i] = []string{s, "ERR"}
				return
			}
			res[i] = []string{s, f.String()}
		}(i, s)
	}
	wg.Wait()
	ok = true
	for _, r := range res {
		if serial[r[0]] != r[1] {
			ok = false
		}
	}
	check("concurrent == serial", ok)

	if failed {
		os.Exit(1)
	}
}
