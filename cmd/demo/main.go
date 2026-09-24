package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/seq"
	"ontology/tob"
)

func main() {
	fails := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK: " + name)
		} else {
			fails++
			fmt.Println("FAIL: " + name)
		}
	}

	// 1) seq 包：分配 1,2 与空洞区间 (d,next) 判定。
	a := seq.New()
	x1, x2 := a.Alloc(), a.Alloc()
	a.Alloc()
	check(fmt.Sprintf("seq alloc=%d,%d next=%d IsGap(3|d=2)=%v", x1, x2, a.Next(), a.IsGap(3, 2)),
		x1 == 1 && x2 == 2 && a.Next() == 4 && a.IsGap(3, 2) && !a.IsGap(2, 2))

	// 2) 第三节十步：逐步记录 (deliveredUpTo,nextSeq)，崩溃后展示空洞。
	s := api.New()
	n := 1
	prop := func(p string) { v, _ := s.Propose(p); n = v + 1 }
	dn := func() string { return fmt.Sprintf("(%d,%d)", s.Delivered(), n) }
	traj := []string{}
	step := func(fn func()) { fn(); traj = append(traj, dn()) }
	step(func() { prop("A") })
	step(func() { prop("B") })
	step(func() { s.Deliver() })
	step(func() { prop("C") })
	step(func() { s.Deliver() })
	step(func() { prop("D") })
	step(func() { prop("E") })
	step(func() { s.Crash() })
	_, _, blocked := s.Deliver() // 空洞未补：必须停住、不跳号
	step(func() { s.RePropose(3, "C"); s.RePropose(4, "D"); s.RePropose(5, "E") })
	step(func() { s.Deliver(); s.Deliver(); s.Deliver() })
	wantTraj := "(0,2)(0,3)(1,3)(1,4)(2,4)(2,5)(2,6)(2,6)(2,6)(5,6)"
	check("ten steps (d,n)="+strings.Join(traj, "")+" gap after crash={3,4,5}, Deliver blocked="+fmt.Sprint(!blocked),
		strings.Join(traj, "") == wantTraj && !blocked && s.Delivered() == 5)

	// 3) 乱序补发后最终投递序列必须为 A,B,C,D,E。
	s2 := api.New()
	for _, p := range []string{"A", "B", "C", "D", "E"} {
		s2.Propose(p)
	}
	s2.Deliver()
	s2.Deliver()
	s2.Crash()
	s2.RePropose(4, "D") // 乙：打乱顺序 4,3,5
	s2.RePropose(3, "C")
	s2.RePropose(5, "E")
	tail := []string{}
	for i := 0; i < 3; i++ {
		_, p, ok := s2.Deliver()
		if ok {
			tail = append(tail, p)
		}
	}
	full := append([]string{"A", "B"}, tail...) // A,B 崩溃前已投，C,D,E 补发后投
	check("out-of-order refill; final delivery="+strings.Join(full, ",")+" contiguous",
		strings.Join(full, ",") == "A,B,C,D,E" && s2.Delivered() == 5)

	// 4) 四类互不相同错误；被拒不留痕，之后仍可正常使用。
	s3 := api.New()
	for _, p := range []string{"A", "B", "C", "D", "E"} {
		s3.Propose(p)
	}
	s3.Deliver()
	s3.Deliver()
	s3.Crash()
	d0 := s3.Delivered()
	_, eEmpty := s3.Propose("")
	eDel := s3.RePropose(2, "x")
	eRange := s3.RePropose(6, "x")
	s3.RePropose(3, "C")
	eFilled := s3.RePropose(3, "X")
	distinct := errors.Is(eEmpty, api.ErrEmptyPayload) && errors.Is(eDel, api.ErrAlreadyDelivered) &&
		errors.Is(eRange, api.ErrSeqOutOfRange) && errors.Is(eFilled, api.ErrSlotFilled) &&
		eEmpty != eDel && eDel != eRange && eRange != eFilled
	_, stillOK := s3.Propose("z")
	check("four distinct reject errors, state unchanged (d="+fmt.Sprint(d0)+"), usable after",
		distinct && s3.Delivered() == d0 && stillOK == nil)

	// 5) 大 m 下单次 Deliver 扫过条目数恒为 1（结论由同包 SelfCheck 内部核验，不暴露计数器）。
	check("O(1) deliver: scan==1 for m=100,1000,10000 (via tob.SelfCheck)", tob.SelfCheck() == nil)

	// 6) 并发 Propose：seq 为 1..N 连续双射，随后 N 次 Deliver 严格按序。
	const N = 300
	c := api.New()
	seqs := make([]int, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); v, _ := c.Propose(fmt.Sprintf("m%d", i)); seqs[i] = v }(i)
	}
	wg.Wait()
	bij := true
	seen := make([]bool, N+1)
	for _, v := range seqs {
		if v < 1 || v > N || seen[v] {
			bij = false
		}
		seen[v] = true
	}
	inOrder := true
	for k := 1; k <= N; k++ {
		if q, _, ok := c.Deliver(); !ok || q != k {
			inOrder = false
		}
	}
	check(fmt.Sprintf("concurrent Propose bijection 1..%d and in-order delivery", N), bij && inOrder)

	// 7) 对外自检整体通过。
	check("api.SelfCheck (all four invariants)", api.New().SelfCheck() == nil)

	if fails > 0 {
		fmt.Printf("FAILED %d check(s)\n", fails)
		os.Exit(1)
	}
}
