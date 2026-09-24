package main

import (
	"fmt"
	"sync"

	"ontology/api"
	"ontology/tob"
)

func ok(name string, cond bool) {
	if cond {
		fmt.Printf("OK %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
	}
}

// apiFacadeOK 验证 api 门面：初始游标为 0，SelfCheck 不 panic 即通过。
func apiFacadeOK() (good bool) {
	good = true
	defer func() {
		if recover() != nil {
			good = false
		}
	}()
	a := api.New()
	if a.Delivered() != 0 {
		return false
	}
	a.SelfCheck()
	return
}

func main() {
	// seq 判定：Propose 返回 1,2,3 严格递增（seq 包经 tob 间接驱动）。
	l0 := tob.New()
	a, _ := l0.Propose("a")
	b, _ := l0.Propose("b")
	c, _ := l0.Propose("c")
	ok("seq alloc 1,2,3 strict", a == 1 && b == 2 && c == 3)

	// 第三节十步：d 取 Delivered()，n=末次 Propose 返回 seq+1。
	l := tob.New()
	n := 1
	prop := func(p string) { s, _ := l.Propose(p); n = s + 1 }
	dn := func() string { return fmt.Sprintf("%d/%d", l.Delivered(), n) }
	var st []string
	prop("A")
	st = append(st, dn())
	prop("B")
	st = append(st, dn())
	l.Deliver()
	st = append(st, dn())
	prop("C")
	st = append(st, dn())
	l.Deliver()
	st = append(st, dn())
	prop("D")
	st = append(st, dn())
	prop("E")
	st = append(st, dn())
	l.Crash()
	st = append(st, "crash"+dn())
	for _, rp := range []struct {
		s int
		p string
	}{{4, "D"}, {3, "C"}, {5, "E"}} { // 乱序补发
		if err := l.RePropose(rp.s, rp.p); err != nil {
			ok("repropose "+rp.p, false)
		}
	}
	st = append(st, dn())
	var got []string
	for range 3 {
		if _, p, delivered := l.Deliver(); delivered {
			got = append(got, p)
		}
	}
	st = append(st, dn())
	wantSt := []string{"0/2", "0/3", "1/3", "1/4", "2/4", "2/5", "2/6", "crash2/6", "2/6", "5/6"}
	ok("10-step d/n sequence", fmt.Sprint(st) == fmt.Sprint(wantSt))
	ok("crash gap filled, final A,B,C,D,E", fmt.Sprint(got) == "[C D E]" && l.Delivered() == 5)

	// 四类可判定哨兵错误：在带空洞的定序器上逐一触发，互不相同。
	q := tob.New()
	q.Propose("m")
	q.Deliver() // d=1
	q.Propose("z")
	q.Crash() // 槽位 2 丢失，n=3
	_, eEmpty := q.Propose("")
	eDelivered := q.RePropose(1, "x")            // seq ≤ d
	eRange := q.RePropose(99, "x")               // seq ≥ n
	if err := q.RePropose(2, "z2"); err != nil { // 先成功填回槽位 2
		ok("fill gap slot 2", false)
	}
	eFilled := q.RePropose(2, "dup") // 槽位已填
	distinct := map[error]int{}
	for _, e := range []error{eEmpty, eDelivered, eRange, eFilled} {
		distinct[e]++
	}
	ok("4 distinct sentinel errors", len(distinct) == 4)

	// 失败不留痕：补发成功内容 z2 可按序投递，定序器继续正常工作。
	_, p, delivered := q.Deliver()
	ok("rejected ops leave no trace", delivered && p == "z2" && q.Delivered() == 2)

	// 全不变量（含大 m 下单次 Deliver 扫过条目恒为 1）由 SelfCheck 内部核验，计数不外露。
	ok("selfcheck incl O(1) scan==1", tob.New().SelfCheck() == nil)
	ok("api facade New/SelfCheck", apiFacadeOK())

	// 并发 Propose：N goroutine，seq 为 1..N 连续双射；随后投递也是 1..N。
	const N = 200
	r := tob.New()
	seqs := make(chan int, N)
	var wg sync.WaitGroup
	for i := range N {
		wg.Add(1)
		go func(i int) { defer wg.Done(); s, _ := r.Propose(fmt.Sprintf("p%d", i)); seqs <- s }(i)
	}
	wg.Wait()
	close(seqs)
	seen := make(map[int]bool, N)
	for s := range seqs {
		seen[s] = true
	}
	bijection := len(seen) == N
	for s := 1; s <= N; s++ {
		bijection = bijection && seen[s]
	}
	inOrder := true
	for want := 1; want <= N; want++ {
		s, _, delivered := r.Deliver()
		inOrder = inOrder && delivered && s == want
	}
	ok("concurrent Propose 1..200 bijection", bijection && inOrder && r.Delivered() == N)
}
