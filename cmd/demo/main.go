package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"

	"ontology/api"
	"ontology/state"
	"ontology/ttl"
)

var fails int

func ok(name string, cond bool, detail string) {
	if cond {
		fmt.Printf("OK   %s %s\n", name, detail)
	} else {
		fmt.Printf("FAIL %s %s\n", name, detail)
		fails++
	}
}

func get(st *state.Table, k string, now int64) string {
	v, hit, _ := st.Get(k, now)
	if hit {
		return "hit:" + v
	}
	return "miss"
}

func main() {
	// ttl 包：now-last==ttl 边界算过期
	ok("ttl-boundary", ttl.Expired(110, 100, 10) && !ttl.Expired(109, 100, 10),
		"now-last==ttl expired")

	// state 包：第三节八步轨迹（ttl=10）
	st := state.New(10)
	st.Put("k1", "A", 100)
	st.Put("k2", "B", 110)
	s3 := get(st, "k1", 109)
	s4 := get(st, "k1", 110)
	st.Put("k3", "C", 105)
	rm6 := st.Cleanup(115)
	s7 := get(st, "k2", 120)
	st.Put("k1", "D", 200)
	ok("eight-step", s3 == "hit:A" && s4 == "miss" && rm6 == 1 && s7 == "miss",
		fmt.Sprintf("s3=%s s4=%s s6=rm%d s7=%s", s3, s4, rm6, s7))

	// state 包：Cleanup 只清过期条目
	st2 := state.New(10)
	st2.Put("a", "x", 100)
	st2.Put("b", "y", 105)
	st2.Put("c", "z", 110)
	rm := st2.Cleanup(115)
	ok("cleanup-only-expired", rm == 2 && get(st2, "c", 115) == "hit:z",
		fmt.Sprintf("removed=%d survivor=c", rm))

	// state 包：last 单调，迟到事件不回退
	st3 := state.New(10)
	st3.Put("k", "A", 200)
	st3.Put("k", "B", 50)
	ok("last-monotonic", get(st3, "k", 205) == "hit:B", "late event kept last=200")

	// api 包：两类可判定错误，互不相同
	tb, _ := api.New(10)
	errPut := tb.Put("", "x", 1)
	_, _, errGet := tb.Get("", 0)
	ok("err-empty-key", errors.Is(errPut, state.ErrEmptyKey) && errors.Is(errGet, state.ErrEmptyKey),
		"Put/Get empty key rejected")
	_, err0 := api.New(0)
	_, errNeg := api.New(-7)
	ok("err-invalid-ttl", errors.Is(err0, api.ErrInvalidTTL) && errors.Is(errNeg, api.ErrInvalidTTL) &&
		api.ErrInvalidTTL != state.ErrEmptyKey, "New(ttl<=0) rejected, distinct sentinel")

	// api 包：被拒后状态不变
	tb.Put("k1", "A", 100)
	tb.Put("", "x", 999)
	_, hit105, _ := tb.Get("k1", 105)
	_, hit110, _ := tb.Get("k1", 110)
	ok("reject-no-trace", hit105 && !hit110, "state unchanged after rejected Put")

	// 大 m 下 Cleanup 清除数不随 m 增长（遍历上界由 TestCleanupScanBounded 钉住）
	same := true
	for _, m := range []int{100, 1000, 10000} {
		t, _ := api.New(10)
		for i := 0; i < m; i++ {
			t.Put("k"+strconv.Itoa(i), "v", int64(i))
		}
		same = same && t.Cleanup(12) == 3 // 仅 last=0,1,2 过期
	}
	ok("cleanup-independent-of-m", same, "removed=3 for m=100..10000")

	// 并发：Put 与 Get/Cleanup 并发，Get 不得返回已过期旧值
	tbc, _ := api.New(10)
	var wg sync.WaitGroup
	start := make(chan struct{})
	stale := make(chan string, 16)
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 2000; i++ {
			tbc.Put("k"+strconv.Itoa(i), strconv.Itoa(i), int64(i))
		}
	}()
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			<-start
			for i := 0; i < 2000; i++ {
				now := int64((i*7 + r*13) % 2050)
				if i%5 == 0 {
					tbc.Cleanup(now)
					continue
				}
				if v, hit, _ := tbc.Get("k"+strconv.Itoa(i), now); hit {
					et, _ := strconv.Atoi(v)
					if now-int64(et) >= 10 {
						stale <- "stale hit"
					}
				}
			}
		}(r)
	}
	close(start)
	wg.Wait()
	close(stale)
	ok("concurrent-get-consistent", len(stale) == 0, "no expired value returned")

	// api 包：SelfCheck 核验四条不变量
	ok("selfcheck", tbc.SelfCheck() == nil, "four invariants verified")

	if fails > 0 {
		os.Exit(1)
	}
}
