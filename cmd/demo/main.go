package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/bank"
	"ontology/check"
	"ontology/vec"
)

var pass, n int

func judge(name string, ok bool) {
	n++
	word := "FAIL"
	if ok {
		pass++
		word = "OK"
	}
	fmt.Printf("%s %s\n", word, name)
}

func main() {
	le, _ := vec.LE(vec.V{1, 2}, vec.V{2, 2})
	sum, _ := vec.Add(vec.V{1, 2}, vec.V{3, 4})
	_, dim := vec.Sub(vec.V{1}, vec.V{1, 2})
	judge("vec 逐分量比较/加减与维度错误", le && sum[0] == 4 && sum[1] == 6 && errors.Is(dim, vec.ErrDim))
	b := bank.New(vec.V{6, 6})
	ok := b.Declare(0, vec.V{3, 3}) == nil && b.Declare(1, vec.V{3, 3}) == nil && b.Declare(2, vec.V{4, 4}) == nil
	judge("bank 三个作业声明成功", ok)
	ok = b.Request(0, vec.V{2, 1}) == nil && b.Request(1, vec.V{1, 2}) == nil && b.Request(2, vec.V{1, 1}) == nil
	judge("bank 三笔安全申请全部批准", ok)
	err := b.Request(2, vec.V{1, 1})
	_, _, alloc := b.Snapshot()
	judge("bank 不安全申请 ErrUnsafe 且状态不变", errors.Is(err, bank.ErrUnsafe) && alloc[2][0] == 1 && alloc[2][1] == 1)
	avail, max, _ := b.Snapshot()
	tAvail, _ := vec.Sub(avail, vec.V{1, 1})
	tAlloc := map[int]vec.V{0: alloc[0], 1: alloc[1], 2: {2, 2}}
	judge("check 朴素参照同样判定试算不安全", !check.Safe(tAvail, max, tAlloc))
	ok = errors.Is(b.Request(0, vec.V{9, 9}), bank.ErrExceedsClaim) && errors.Is(b.Release(0, vec.V{9, 9}), bank.ErrOverRelease)
	judge("bank 哨兵错误可 errors.Is 区分", ok && errors.Is(b.Request(9, vec.V{1, 1}), bank.ErrUnknown))
	b.Finish(2)
	avail, _, _ = b.Snapshot()
	judge("bank Finish 归还全部资源", avail[0] == 3 && avail[1] == 3 && check.Consistent(b, vec.V{6, 6}))
	word := "FAIL"
	if pass == n {
		word = "OK"
	}
	fmt.Printf("%s %d/%d 项通过\n", word, pass, n)
	if pass != n {
		os.Exit(1)
	}
}
