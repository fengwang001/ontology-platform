// Command demo 对布隆过滤器各包做冒烟判定，每行输出 OK/FAIL。
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/bits"
	"ontology/bloom"
	"ontology/check"
)

var passed, total int

func report(name string, ok bool) {
	total++
	status := "FAIL"
	if ok {
		passed++
		status = "OK"
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	bs := bits.New(128)
	ok := bs.Set(7) == nil && bs.Len() == 128
	got, err := bs.Get(7)
	report("bits set/get", ok && err == nil && got)

	f, err := bloom.New(10000, 0.01, 42)
	report("bloom new", err == nil && f.K() > 1)
	for i := 0; i < 10000; i++ {
		f.Add([]byte(fmt.Sprintf("value-%d", i)))
	}
	report("no false negative", f.MaybeContains([]byte("value-9999")))
	_, err = bloom.New(0, 0.01, 1)
	report("bad param", errors.Is(err, bloom.ErrBadParam))
	var empty bloom.Filter
	report("empty filter safe", !empty.MaybeContains([]byte("x")))
	f.MaybeContains([]byte("probe"))
	report("reads == k", f.Reads() == f.K())
	fp := 0
	for i := 0; i < 10000; i++ {
		if f.MaybeContains([]byte(fmt.Sprintf("unseen-%d", i))) {
			fp++
		}
	}
	report("fp rate <= 0.02", fp <= 200)

	s := check.NewSet()
	for i := 0; i < 10000; i++ {
		s.Add([]byte(fmt.Sprintf("value-%d", i)))
	}
	report("naive cross-check", check.Verify(f, s) == nil)

	fmt.Printf("%s total %d/%d\n", map[bool]string{true: "OK", false: "FAIL"}[passed == total], passed, total)
	if passed != total {
		os.Exit(1)
	}
}
