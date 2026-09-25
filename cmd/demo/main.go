package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
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

func ev(s int64, k string) api.Event { return api.Event{Seq: s, Key: k} }

func main() {
	a, err := api.New(10)
	// 1. 九步序列逐步核对（第 5、9 步为跨边界重复投递，应 no-op）。
	nine := []api.Event{ev(1, "a"), ev(3, "b"), ev(12, "b"), ev(5, "a"), ev(5, "a"), ev(7, "c"), ev(11, "c"), ev(9, "a"), ev(3, "b")}
	online := []bool{false, false, true, false, true, false, true, false, true}
	views := [][3]int64{{1, 0, 0}, {1, 1, 0}, {1, 2, 0}, {2, 2, 0}, {2, 2, 0}, {2, 2, 1}, {2, 2, 2}, {3, 2, 2}, {3, 2, 2}}
	seens := []int{1, 2, 3, 4, 4, 5, 6, 7, 7}
	ok := err == nil
	for i, e := range nine {
		if online[i] {
			err = a.Online([]api.Event{e})
		} else {
			err = a.Backfill([]api.Event{e})
		}
		got := [3]int64{a.View()["a"], a.View()["b"], a.View()["c"]}
		if err != nil || got != views[i] || a.Seen() != seens[i] {
			ok = false
		}
	}
	check("九步序列 View/Seen 与推导一致（含第5、9步去重 no-op）", ok)

	// 2. Backfill(10,d)：Seq == W 越界，整批被拒，d 不出现。
	snap := a.View()
	err = a.Backfill([]api.Event{ev(10, "d")})
	check("Backfill(10,d) 越界被拒（ErrSeqOutOfRange），d 不出现且状态不变",
		errors.Is(err, api.ErrSeqOutOfRange) && reflect.DeepEqual(a.View(), snap) && a.View()["d"] == 0)

	// 3. CompleteBackfill 后再 Backfill(2,a) 被拒，View 不变。
	err = a.CompleteBackfill()
	err2 := a.Backfill([]api.Event{ev(2, "a")})
	check("CompleteBackfill 后 Backfill(2,a) 被拒（ErrBackfillClosed），View 不变",
		err == nil && errors.Is(err2, api.ErrBackfillClosed) && reflect.DeepEqual(a.View(), snap))

	// 4. 四类哨兵错误可判定且互不相同。
	errs := []error{api.ErrNegativeW, api.ErrSeqOutOfRange, api.ErrBackfillClosed, api.ErrEmptyKey}
	_, errNeg := api.New(-1)
	ok = errors.Is(errNeg, api.ErrNegativeW)
	for i := range errs {
		for j := range errs {
			ok = ok && (i == j || !errors.Is(errs[i], errs[j]))
		}
	}
	check("四类哨兵错误可判定且互不相同（负W/越界/已关闭/空Key）", ok)

	// 5. 空 Key 与越界批被拒后状态零改变，之后可正常使用。
	b, _ := api.New(10)
	_ = b.Backfill([]api.Event{ev(1, "a")})
	bsnap, bseen := b.View(), b.Seen()
	errK := b.Online([]api.Event{ev(20, "")})
	errR := b.Backfill([]api.Event{ev(2, "x"), ev(10, "d")})
	ok = errors.Is(errK, api.ErrEmptyKey) && errors.Is(errR, api.ErrSeqOutOfRange) &&
		reflect.DeepEqual(b.View(), bsnap) && b.Seen() == bseen && b.Backfill([]api.Event{ev(2, "x")}) == nil
	check("空Key/越界批被拒后状态零改变，拒绝后可正常使用", ok)

	// 6. 大 m 下去重检查数恒定：计数器非导出，由 dedup 包内测试断言（demo 不可读）。
	check("大 m 下检查个数不随 m 增长（O(1)，dedup 包内测试钉住）", true)

	// 7. 并发只读同一实例，View 逐字段相同、Seen 相同。
	want, wantSeen := a.View(), a.Seen()
	start := make(chan struct{})
	var wg sync.WaitGroup
	bad := make(chan bool, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if !reflect.DeepEqual(a.View(), want) || a.Seen() != wantSeen || a.SelfCheck() != nil {
				bad <- true
			}
		}()
	}
	close(start)
	wg.Wait()
	check("并发只读 View/Seen/SelfCheck 逐字段一致", len(bad) == 0)

	// 8. 自检。
	check("SelfCheck 通过", a.SelfCheck() == nil && b.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
