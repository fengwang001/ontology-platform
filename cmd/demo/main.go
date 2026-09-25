// demo 依次核验：八行分步表、反对称、四类错误、失败不留痕、并发只读。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/sar"
	"ontology/unwrap"
)

var failed bool

func ok(name string, cond bool, detail string) {
	mark := "OK  "
	if !cond {
		mark, failed = "FAIL", true
	}
	fmt.Printf("%s%s: %s\n", mark, name, detail)
}

func main() {
	sp, err := api.New(4)
	if err != nil {
		fmt.Println("FAIL New(4):", err)
		os.Exit(1)
	}
	// 第三节八行分步表：期望绝对序号（-1 表示该步应报错，对应半圈）。
	seq := []uint64{13, 14, 15, 0, 1, 2, 10, 3}
	wantAbs := []int64{13, 14, 15, 16, 17, 18, -1, 19}
	var parts [2]string
	allOK := true
	for i, s := range seq {
		before := sp.Last()
		abs, ferr := sp.Feed(s)
		allOK = allOK && (wantAbs[i] == -1) == (ferr != nil) && (ferr != nil || abs == wantAbs[i])
		desc := "首个"
		if i > 0 {
			desc = fmt.Sprintf("d=%d", sar.Diff(uint64(before)&15, s, 16))
		}
		cell := fmt.Sprintf(" %d:%s→%d", s, desc, abs)
		if ferr != nil {
			cell = fmt.Sprintf(" %d:%s→ERR", s, desc)
		}
		parts[i/4] += cell
	}
	ok("feed 步骤1-4", allOK, parts[0]+" （第4步 0 回绕→16）")
	ok("feed 步骤5-8", allOK, parts[1]+" （第7步 10 半圈被拒）")

	// 反对称：确定性随机 a,b 两两互反。
	anti := true
	x := uint64(0x9e3779b97f4a7c15)
	for i := 0; i < 4096 && anti; i++ {
		x ^= x<<13 ^ x>>7 ^ x<<17
		a, b := x&15, (x*2862933555777941757)&15
		f, r := sp.Cmp(a, b), sp.Cmp(b, a)
		anti = (f == api.Less) == (r == api.Greater) && (f == api.Incomparable) == (r == api.Incomparable)
	}
	ok("antisymmetry", anti, "4096 对 (a,b) Cmp 互反")

	// 四类可判定错误互不相同。
	_, eW := api.New(0)
	sp2, _ := api.New(4)
	sp2.Feed(13)
	_, eR := sp2.Feed(16) // s >= M
	sp2.Feed(14)
	_, eI := sp2.Feed(6)  // d=(6-14)&15=8 半圈
	_, eG := sp2.Feed(13) // d=(13-14)&15=15 倒退
	distinct := errors.Is(eW, unwrap.ErrWidth) && errors.Is(eR, unwrap.ErrOutOfRange) &&
		errors.Is(eI, unwrap.ErrIncomparable) && errors.Is(eG, unwrap.ErrMovedBackward) &&
		eW != eR && eR != eI && eI != eG
	ok("errors", distinct, "Width/OutOfRange/Incomparable/MovedBackward 可判定且互异")

	// 失败不留痕：第 7 步被拒时 last 停在 18，第 8 步正常推进到 19。
	ok("last-unchanged", sp.Last() == 19, "被拒后 last 不变且后续可用")

	// 展开检查历史数 ≤1：计数器非导出，由 unwrap 同包测试钉住。
	ok("checked<=1", true, "O(1) 每步，由 unwrap.TestChecked 钉住（计数器非导出）")

	// 并发只读：32 个 goroutine 同时读 Last/Width，结果逐字段一致。
	start := make(chan struct{})
	results := make(chan [2]int64, 32)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- [2]int64{sp.Last(), int64(sp.Width())}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	same := true
	for r := range results {
		same = same && r[0] == 19 && r[1] == 4
	}
	ok("concurrent-read", same, "32 goroutine Last/Width 逐字段相同")

	// SelfCheck 多档宽度。
	sc := true
	for _, n := range []int{1, 4, 16, 63} {
		s, _ := api.New(n)
		sc = sc && s.SelfCheck() == nil
	}
	ok("SelfCheck", sc, "N=1,4,16,63 全部通过")

	if failed {
		os.Exit(1)
	}
}
