package main

import (
	"errors"
	"fmt"
	"math/bits"
	"os"
	"reflect"
	"slices"
	"sync"

	"ontology/api"
	"ontology/lsn"
)

var failed bool

func report(name string, ok bool, detail string) {
	s := "OK"
	if !ok {
		s = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s %s\n", s, name, detail)
}

// 第三节八步：逐步空洞、FindNew(15)、FindOld(0)、第 6 步重复被拒、互逆可寻址。
func checkEightSteps() (holesOK, invOK bool, holesStr string, fn, fo int64, dupErr error) {
	l := api.New()
	ops := []int64{10, 13, 15, 12, 17, 10, 20, 18}
	want := [][]int64{{}, {11, 12}, {11, 12, 14}, {11, 14}, {11, 14, 16}, {11, 14, 16}, {11, 14, 16, 18, 19}, {11, 14, 16, 19}}
	parts := make([]string, 0, 8)
	holesOK, invOK = true, true
	for i, v := range ops {
		err := l.Append(v)
		if i == 5 {
			dupErr = err // 与第 1 步重复，必须被拒
		} else if err != nil {
			holesOK = false
		}
		holesOK = holesOK && slices.Equal(l.Holes(), want[i])
		parts = append(parts, fmt.Sprint(l.Holes()))
	}
	fn, _ = l.FindNew(15)
	fo, _ = l.FindOld(0)
	for k := 0; k < l.Count(); k++ { // 互逆：FindNew(FindOld(k))==k 且 FindOld(FindNew(x))==x
		old, e1 := l.FindOld(int64(k))
		back, e2 := l.FindNew(old)
		again, e3 := l.FindOld(back)
		invOK = invOK && e1 == nil && e2 == nil && e3 == nil && back == int64(k) && again == old
	}
	return holesOK, invOK, fmt.Sprint(parts), fn, fo, dupErr
}

// 四类可判定错误互不相同，且被拒后状态不变、仍可正常使用。
func checkErrors() (errOK, stateOK bool) {
	l := api.New()
	_ = l.Append(10)
	_ = l.Append(20)
	n0, h0 := l.Count(), l.Holes()
	e1 := l.Append(-1)
	e2 := l.Append(10)
	_, e3 := l.FindNew(15)
	_, e4 := l.FindOld(-1)
	errOK = errors.Is(e1, api.ErrNegative) && errors.Is(e2, api.ErrDuplicate) &&
		errors.Is(e3, api.ErrUnknown) && errors.Is(e4, api.ErrOutOfRange)
	seen := map[error]bool{}
	for _, e := range []error{api.ErrNegative, api.ErrDuplicate, api.ErrUnknown, api.ErrOutOfRange} {
		errOK = errOK && !seen[e]
		seen[e] = true
	}
	stateOK = l.Count() == n0 && slices.Equal(l.Holes(), h0) && l.Append(15) == nil
	return errOK, stateOK
}

// 大 m 下二分定位的比较个数不随 m 线性增长。
// 计数器是非导出字段，只能在这里用 reflect 直接读字段（不经任何导出函数）。
func checkCompares() (bool, string) {
	for _, m := range []int{100, 1000, 5000, 10000} {
		s := lsn.New()
		for i := 0; i < m; i++ { // 间隔 2 的 LSN，人为造空洞
			if err := s.Add(int64(i * 2)); err != nil {
				return false, "add failed"
			}
		}
		if _, err := s.IndexOf(2 * int64(m/3)); err != nil {
			return false, "find failed"
		}
		cmps := int(reflect.ValueOf(s).Elem().FieldByName("lastCmps").Int())
		if cmps > bits.Len(uint(m-1))+2 || cmps >= 32 {
			return false, fmt.Sprintf("m=%d cmps=%d", m, cmps)
		}
	}
	return true, "m=100..10000 均 <=log2(m)+2 且 <32"
}

// 并发只读：N 个 goroutine 读同一实例，Holes 与 FindNew 结果逐值相同。
func checkConcurrent() (bool, string) {
	l := api.New()
	for i := 0; i < 200; i++ {
		if err := l.Append(int64(i*2 + 100)); err != nil {
			return false, "add failed"
		}
	}
	h0 := l.Holes()
	var wg sync.WaitGroup
	bad, res := make([]bool, 8), make([][]int64, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				old, e1 := l.FindOld(int64(k % l.Count()))
				nw, e2 := l.FindNew(old)
				if e1 != nil || e2 != nil || !slices.Equal(l.Holes(), h0) {
					bad[i] = true
				}
				res[i] = append(res[i], nw)
			}
		}(i)
	}
	wg.Wait()
	for i := 1; i < 8; i++ {
		bad[0] = bad[0] || !slices.Equal(res[i], res[0])
	}
	if slices.Contains(bad, true) {
		return false, "goroutine 结果不一致"
	}
	return true, "8 goroutine x50 轮逐值相同"
}

func main() {
	holesOK, invOK, holesStr, fn, fo, dupErr := checkEightSteps()
	report("八步空洞:", holesOK, holesStr)
	report("FindNew(15)/FindOld(0):", fn == 3 && fo == 10, fmt.Sprintf("=%d/%d", fn, fo))
	report("第6步重复被拒:", errors.Is(dupErr, api.ErrDuplicate), fmt.Sprint(dupErr))
	report("互逆可寻址:", invOK, "n=7 双向互逆")
	errOK, stateOK := checkErrors()
	report("四类可判定错误:", errOK, "负/重复/未知/越界 四种哨兵")
	report("被拒后状态不变:", stateOK, "")
	ok, detail := checkCompares()
	report("比较个数不随m增长:", ok, detail)
	ok, detail = checkConcurrent()
	report("并发只读一致:", ok, detail)
	report("SelfCheck:", api.New().SelfCheck() == nil, "")
	if failed {
		os.Exit(1)
	}
}
