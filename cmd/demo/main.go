// demo 演示可插入排序键生成器的全部约定：三种插入位置、确定性、
// 长度上限与重排、并发插入的确定顺序、三类非法输入。
// 不读命令行参数、不联网；全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"ontology/sortkey"
)

var passed, failed int

// check 打印一行以 OK 或 FAIL 开头的判定。
func check(ok bool, format string, args ...any) {
	mark := "OK  "
	if !ok {
		mark = "FAIL"
		failed++
	} else {
		passed++
	}
	fmt.Printf("%s %s\n", mark, fmt.Sprintf(format, args...))
}

func main() {
	gen := sortkey.NewFractional(12)

	// 1. 三种插入位置。
	s := sortkey.NewSequence(gen)
	kM, _ := s.Insert("", "", "m")
	kF, errF := s.Insert("", kM, "front")
	kB, errB := s.Insert(kM, "", "back")
	kX, errX := s.Insert(kF, kM, "mid")
	check(errF == nil && kF < kM, "最前插入: 键 %q 落在 (-∞, %q)", kF, kM)
	check(errB == nil && kM < kB, "最后插入: 键 %q 落在 (%q, +∞)", kB, kM)
	check(errX == nil && kF < kX && kX < kM, "中间插入: 键 %q 落在 (%q, %q)", kX, kF, kM)

	// 2. 确定性：同一对邻居反复调用结果一致。
	det := true
	for i := 0; i < 5; i++ {
		k, err := gen.Between(kF, kM)
		det = det && err == nil && k == kX
	}
	check(det, "确定性: 对 (%q, %q) 调用 5 次均得 %q", kF, kM, kX)

	// 3. 连续在同一间隙插入直到触发"需要重排"。
	tight := sortkey.NewSequence(sortkey.NewFractional(12))
	_, _ = tight.Insert("", "", "anchor")
	inserts, loopErr := 0, error(nil)
	for i := 0; ; i++ {
		right := tight.Snapshot()[0].Key
		if _, loopErr = tight.Insert("", right, fmt.Sprintf("v%03d", i)); loopErr != nil {
			break
		}
		inserts++
	}
	st := tight.Stats()
	check(errors.Is(loopErr, sortkey.ErrNeedsRebalance),
		"长度上限: 同间隙插入 %d 次后触发重排错误, 最长键 %d/%d", inserts, st.Longest, st.Limit)

	// 4. 重排：相对顺序不变，键重新变短。
	before := valuesOf(tight.Snapshot())
	rebErr := tight.Rebalance()
	after := tight.Snapshot()
	sameOrder := equalStrings(before, valuesOf(after))
	newKeys := make([]string, 0, 3)
	for i := 0; i < 3 && i < len(after); i++ {
		newKeys = append(newKeys, fmt.Sprintf("%q", after[i].Key))
	}
	check(rebErr == nil && sameOrder && tight.SelfCheck() == nil,
		"重排: 相对顺序不变(%d 个元素), 新键等距如 %s, 自检通过", len(after), strings.Join(newKeys, " "))

	// 5. 并发插入同一间隙：最终顺序由内容字典序决定。
	con := sortkey.NewSequence(sortkey.NewFractional(64))
	kL, _ := con.Insert("", "", "L")
	kR, _ := con.Insert(kL, "", "R")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = con.Insert(kL, kR, fmt.Sprintf("v%02d", i))
		}(i)
	}
	wg.Wait()
	got := valuesOf(con.Snapshot())
	want := append([]string{"L"}, sortedVals(16)...)
	want = append(want, "R")
	check(equalStrings(got, want) && con.SelfCheck() == nil,
		"并发插入: 16 个 goroutine 同间隙, 最终顺序 %v, 自检通过", got)

	// 6. 三类非法输入，错误类别可区分。
	_, errOrder := gen.Between("b", "a")
	check(errors.Is(errOrder, sortkey.ErrInvalidOrder) && !errors.Is(errOrder, sortkey.ErrNeedsRebalance),
		"非法输入-顺序: Between(\"b\",\"a\") -> %v", errOrder)
	_, errChar := gen.Between("a!", "b")
	var ikErr *sortkey.InvalidKeyError
	check(errors.As(errChar, &ikErr) && ikErr.Char == '!',
		"非法输入-字符: Between(\"a!\",\"b\") -> %v", errChar)
	check(errors.Is(loopErr, sortkey.ErrNeedsRebalance) && !errors.Is(loopErr, sortkey.ErrInvalidOrder),
		"非法输入-重排: 长度超限 -> %v", loopErr)

	// 总计。
	total := passed + failed
	if failed > 0 {
		fmt.Printf("FAIL 总计: %d/%d 通过\n", passed, total)
		os.Exit(1)
	}
	fmt.Printf("OK   总计: %d/%d 全部通过\n", passed, total)
}

func valuesOf(entries []sortkey.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Value
	}
	return out
}

func sortedVals(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("v%02d", i)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
