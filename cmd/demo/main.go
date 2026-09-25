package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func main() {
	st, err := api.New(8)
	check("New(8)", err == nil)

	// 第三节八步序列：逐步核对 ver 与读结果。
	st.Write("a", 10) // ver=1
	st.Write("b", 20) // ver=2
	s1, _ := st.Snapshot()
	st.Write("a", 15)         // ver=3
	r5, _ := st.Read(s1, "a") // 第5步
	st.Write("b", 25)         // ver=4
	r7, _ := st.Read(s1, "b") // 第7步
	s2, _ := st.Snapshot()
	check("eight-op: s1=2 r5=10 r7=20 s2=4", s1 == 2 && r5 == 10 && r7 == 20 && s2 == 4)
	check("step5 old value visible (10 not 15)", r5 == 10 && st.ReadCurrent("a") == 15)
	check("step7 boundary <= (20 at ver==snap)", r7 == 20)
	curB, _ := st.Read(s2, "b")
	check("cross-key consistent at s2 (a=15,b=25)", func() bool { a, _ := st.Read(s2, "a"); return a == 15 && curB == 25 }())

	// 四类可判定错误互不相同。
	errs := []error{api.ErrNonPositiveMax, api.ErrEmptyKey, api.ErrInvalidSnapshot, api.ErrSnapshotLimit}
	distinct := map[error]bool{}
	for _, e := range errs {
		distinct[e] = true
	}
	_, e0 := api.New(0)
	e1 := st.Write("", 1)
	_, e2 := st.Read(-1, "a")
	_, e3 := st.Read(99, "a")
	e4 := st.Release(7)
	lim, _ := api.New(1)
	lim.Snapshot()
	_, e5 := lim.Snapshot()
	check("4 distinct decidable errors", len(distinct) == 4 &&
		errors.Is(e0, api.ErrNonPositiveMax) && errors.Is(e1, api.ErrEmptyKey) &&
		errors.Is(e2, api.ErrInvalidSnapshot) && errors.Is(e3, api.ErrInvalidSnapshot) &&
		errors.Is(e4, api.ErrInvalidSnapshot) && errors.Is(e5, api.ErrSnapshotLimit))

	// 被拒后状态不变：ver 仍为 4，快照读结果不变。
	r5b, _ := st.Read(s1, "a")
	r7b, _ := st.Read(s1, "b")
	s3, err := st.Snapshot()
	check("rejected ops leave no trace", err == nil && s3 == 4 && r5b == 10 && r7b == 20)

	// 大 m：写 10000 个版本后按旧快照读仍正确（sub-linear 由 snap 包测试钉住）。
	big, _ := api.New(4)
	bs, _ := big.Snapshot()
	for i := 0; i < 10000; i++ {
		big.Write("k", int64(i))
	}
	br, _ := big.Read(bs, "k")
	check("large-m read at old snapshot == 0", br == 0 && big.ReadCurrent("k") == 9999)

	// 并发：快照 s 后 N 个 goroutine 各写一个 Key；s 内全 0，Current 各异。
	c, _ := api.New(4)
	cs, _ := c.Snapshot()
	const N = 64
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.Write(fmt.Sprintf("k%d", i), int64(i+1))
		}(i)
	}
	wg.Wait()
	ok := true
	for i := 0; i < N; i++ {
		v, _ := c.Read(cs, fmt.Sprintf("k%d", i))
		if v != 0 || c.ReadCurrent(fmt.Sprintf("k%d", i)) != int64(i+1) {
			ok = false
		}
	}
	check("concurrent writes invisible to prior snapshot", ok)

	check("SelfCheck", st.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
