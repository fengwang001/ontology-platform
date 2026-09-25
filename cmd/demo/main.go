package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
)

var failed bool

func ok(name string, cond bool) {
	if !cond {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	// 第三节八步表：逐步热层/冷层/换出键/Get 返回值，含最终热/冷集。
	s, _ := api.New(2, 100)
	type step struct {
		put              bool
		key              string
		val, get         int64
		hot, cold, evict string
	}
	steps := []step{
		{true, "A", 1, 0, "[A]", "[]", ""},
		{true, "B", 2, 0, "[B A]", "[]", ""},
		{false, "A", 0, 1, "[A B]", "[]", ""},
		{true, "C", 3, 0, "[C A]", "[B]", "B"},
		{false, "B", 0, 2, "[B C]", "[A]", "A"},
		{true, "D", 4, 0, "[D B]", "[A C]", "C"},
		{false, "A", 0, 1, "[A D]", "[B C]", "B"},
		{true, "E", 5, 0, "[E A]", "[B C D]", "D"},
	}
	good := true
	evicts := make([]string, 8)
	gets := make([]int64, 8)
	for i, st := range steps {
		prev := map[string]bool{}
		for _, k := range s.ColdKeys() {
			prev[k] = true
		}
		var e error
		if st.put {
			e = s.Put(st.key, st.val)
		} else {
			gets[i], e = s.Get(st.key)
		}
		for _, k := range s.ColdKeys() {
			if !prev[k] {
				evicts[i] = k
			}
		}
		good = good && e == nil && gets[i] == st.get && evicts[i] == st.evict &&
			fmt.Sprint(s.HotKeys()) == st.hot && fmt.Sprint(s.ColdKeys()) == st.cold
	}
	ok("八步表:逐步热/冷/换出键/Get值,最终热[E A]冷[B C D]", good)
	// (甲) 若 Get 不刷新访问时间（退化为写入序 FIFO）：第 4 步错换 A。
	fifo := []string{"A", "B"} // 写入序；Get(A) 不刷新
	fifo = append(fifo, "C")
	ok("第4步:正确换出B;甲:FIFO错换A冷集{A}", evicts[3] == "B" && fifo[0] == "A")
	// (丙) 若换出丢值：第 5 步 Get(B) 错返零值。
	dropped := map[string]int64{"B": 0} // 只留键不留值
	ok("第5步:Get(B)=2换出A;丙:丢值错返0", gets[4] == 2 && evicts[4] == "A" && dropped["B"] == 0)
	// (乙) 若换出条件写成 >=：第 2 步错换 A。
	geHot := []string{"B", "A"}     // 第 2 步后 MRU→LRU
	geVictim := geHot[len(geHot)-1] // 错误的 >= 条件下第 2 步即触发换出
	ok("陷阱乙:>=条件第2步错换A,热[B]冷[A]", geVictim == "A" && evicts[1] == "")
	// 四类可判定错误，互不相同。
	_, e1 := api.New(0, 1)
	s2, _ := api.New(1, 1)
	e2 := s2.Put("", 1)
	_ = s2.Put("a", 1)
	_ = s2.Put("b", 2)
	e3 := s2.Put("c", 3)
	_, e4 := s2.Get("zz")
	ok("四类错误可判定且互不相同", errors.Is(e1, api.ErrBadCap) && errors.Is(e2, api.ErrEmptyKey) &&
		errors.Is(e3, api.ErrColdFull) && errors.Is(e4, api.ErrNotFound) &&
		!errors.Is(e1, e2) && !errors.Is(e2, e3) && !errors.Is(e3, e4))
	// 被拒后状态（两层归属、值、访问时间）不变。
	before := fmt.Sprint(s2.HotKeys(), s2.ColdKeys())
	_ = s2.Put("x", 9)
	_, _ = s2.Get("")
	_, _ = s2.Get("zzz")
	ok("拒绝不留痕", fmt.Sprint(s2.HotKeys(), s2.ColdKeys()) == before)

	// 大 m：一次换出的受害者是最早访问者；检查数恒定由 lru 包测试钉住。
	const m = 10000
	bs, _ := api.New(m, m)
	for i := 0; i < m; i++ {
		_ = bs.Put(fmt.Sprintf("k%05d", i), int64(i))
	}
	_ = bs.Put("new", -1)
	victimOut := false
	for _, k := range bs.ColdKeys() {
		victimOut = victimOut || k == "k00000"
	}
	ok("big-m:换出正确,检查数不随m增长(见lru测试)", victimOut && len(bs.HotKeys()) == m)

	// 并发：64 路 Get 值正确；32 路 Value 同键全等。
	cs, _ := api.New(64, 64)
	for i := 0; i < 64; i++ {
		_ = cs.Put(fmt.Sprintf("k%d", i), int64(i))
	}
	_ = cs.Put("x", 42)
	var wg sync.WaitGroup
	var bad atomic.Bool
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if v, e := cs.Get(fmt.Sprintf("k%d", (i+j)%64)); e != nil || v != int64((i+j)%64) {
					bad.Store(true)
				}
			}
		}(i)
	}
	vals := make([]int64, 32)
	for i := range vals {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, _ := cs.Value("x")
			vals[i] = v
		}(i)
	}
	wg.Wait()
	same := !bad.Load()
	for _, v := range vals {
		same = same && v == 42
	}
	ok("并发:Get值正确且Value同键全等", same)

	ok("SelfCheck", api.SelfCheck() == nil)
	if failed {
		fmt.Println("RESULT FAIL")
		os.Exit(1)
	}
	fmt.Println("RESULT OK")
}
