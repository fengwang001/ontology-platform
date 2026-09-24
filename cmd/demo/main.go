package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"ontology/api"
	"ontology/bidx"
	"ontology/tsl"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	// tsl：八步序列的前缀最大值
	seq := []int64{2, 1, 8, 3, 4, 9, 5, 7}
	wantPM := []int64{2, 2, 8, 8, 8, 9, 9, 9}
	pmOK := true
	prev := seq[0]
	if prev != wantPM[0] {
		pmOK = false
	}
	for i := 1; i < len(seq); i++ {
		prev = tsl.NextPM(prev, seq[i])
		if prev != wantPM[i] || prev < wantPM[i-1] {
			pmOK = false
		}
	}
	check("tsl: 8-step prefix max & monotone", pmOK)

	// bidx：第三节八步之后的 SafeOff(7/8/2) 与 TSAt
	x := bidx.New(8)
	for _, ts := range seq {
		if err := x.Append(ts); err != nil {
			check("bidx: append 8 records", false)
		}
	}
	o7, f7, _ := x.SafeOff(7)
	o8, f8, _ := x.SafeOff(8)
	o2, f2, _ := x.SafeOff(2)
	check("bidx: SafeOff(7/8/2)==1/4/1", f7 && o7 == 1 && f8 && o8 == 4 && f2 && o2 == 1)
	tsOK := true
	for i, ts := range seq {
		got, err := x.TSAt(int64(i))
		if err != nil || got != ts {
			tsOK = false
		}
	}
	check("bidx: TSAt exact per offset", tsOK)

	// bidx：三类可判定错误，互不相同
	_, errRange := x.TSAt(8)
	_, errNeg := x.TSAt(-1)
	errFull := x.Append(0)
	empty := bidx.New(4)
	_, _, errEmpty := empty.SafeOff(0)
	check("bidx: 3 distinct sentinel errors",
		errors.Is(errRange, bidx.ErrOutOfRange) && errors.Is(errNeg, bidx.ErrOutOfRange) &&
			errors.Is(errFull, bidx.ErrFull) && errors.Is(errEmpty, bidx.ErrEmpty) &&
			bidx.ErrOutOfRange != bidx.ErrEmpty && bidx.ErrEmpty != bidx.ErrFull)

	// bidx：被拒后状态不变
	stable := x.Len() == 8
	for i, ts := range seq {
		got, err := x.TSAt(int64(i))
		if err != nil || got != ts {
			stable = false
		}
	}
	o7b, f7b, _ := x.SafeOff(7)
	check("bidx: state unchanged after rejects", stable && f7b && o7b == 1)

	// api：自检四条不变量
	check("api: SelfCheck 4 invariants", api.SelfCheck() == nil)

	// api：并发只读结果逐值一致（无 sleep，WaitGroup 同步）
	ax := api.New(len(seq))
	for _, ts := range seq {
		_ = ax.Append(ts)
	}
	var wg sync.WaitGroup
	concOK := true
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, ts := range seq {
				got, err := ax.TSAt(int64(i))
				if err != nil || got != ts {
					concOK = false
				}
			}
			o, f, err := ax.SafeOff(7)
			if err != nil || !f || o != 1 || ax.Len() != 8 {
				concOK = false
			}
		}()
	}
	wg.Wait()
	check("api: concurrent readers consistent", concOK)

	// 复杂度：比较计数器是非导出字段，demo 不能经公开接口读它，
	// 改为驱动 bidx 白盒测试验证大 N 下比较次数不随 N 线性增长。
	out, err := exec.Command("go", "test", "-run", "TestSafeOffComparesLog", "-count=1", "./bidx/").CombinedOutput()
	check("bidx: big-N compares O(log N)", err == nil && bytes.Contains(out, []byte("ok")))

	if failed {
		os.Exit(1)
	}
}
