// Command demo 对 seqwin 滑动窗口重放检测器做一组自检，
// 逐条打印 8 项语义的 OK/FAIL，全部通过时以退出码 0 结束。
package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/seqwin"
)

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"1 零序列号非法且不改状态", checkZeroInvalid()},
		{"2 窗口右推并逐出旧序列号", checkPushAndEvict()},
		{"3 乱序在窗内先 Fresh 后 Duplicate", checkOutOfOrder()},
		{"4 边界精确 93 在窗 92 太旧", checkBoundary()},
		{"5 大跳不漏判且保留新最大值", checkBigJump()},
		{"6 重复最大值判 Duplicate", checkDuplicateHighest()},
		{"7 位图容量只与窗口宽度有关", checkBoundedMemory()},
		{"8 并发下每个序列号恰 Fresh 一次", checkConcurrent()},
	}

	failed := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("[%s] %s\n", status, c.name)
	}

	if failed == 0 {
		fmt.Println("ALL CHECKS PASSED")
		os.Exit(0)
	}
	fmt.Printf("%d CHECK(S) FAILED\n", failed)
	os.Exit(1)
}

func checkZeroInvalid() bool {
	w := seqwin.New(8)
	if w.Accept(0) != seqwin.Invalid || w.Highest() != 0 || w.Seen(0) {
		return false
	}
	w.Accept(5)
	return w.Accept(0) == seqwin.Invalid && w.Highest() == 5
}

func checkPushAndEvict() bool {
	w := seqwin.New(3)
	if w.Accept(10) != seqwin.Fresh || w.Accept(11) != seqwin.Fresh {
		return false
	}
	return w.Highest() == 11 &&
		w.Accept(9) == seqwin.Fresh &&
		w.Accept(8) == seqwin.TooOld &&
		!w.Seen(8)
}

func checkOutOfOrder() bool {
	w := seqwin.New(8)
	w.Accept(100)
	return w.Accept(97) == seqwin.Fresh &&
		w.Seen(97) &&
		w.Accept(97) == seqwin.Duplicate
}

func checkBoundary() bool {
	w := seqwin.New(8)
	w.Accept(100)
	return w.Accept(93) == seqwin.Fresh &&
		w.Accept(93) == seqwin.Duplicate &&
		w.Accept(92) == seqwin.TooOld
}

func checkBigJump() bool {
	w := seqwin.New(8)
	w.Accept(10)
	return w.Accept(10000) == seqwin.Fresh &&
		w.Seen(10000) &&
		w.Accept(10000) == seqwin.Duplicate &&
		w.Accept(9999) == seqwin.Fresh &&
		w.Accept(10) == seqwin.TooOld
}

func checkDuplicateHighest() bool {
	w := seqwin.New(4)
	w.Accept(42)
	return w.Accept(42) == seqwin.Duplicate &&
		w.Accept(42) == seqwin.Duplicate &&
		w.Highest() == 42
}

func checkBoundedMemory() bool {
	const size = 16
	w := seqwin.New(size)
	for seq := uint64(1); seq <= 100000; seq++ {
		if w.Accept(seq) != seqwin.Fresh {
			return false
		}
	}
	return w.Highest() == 100000 && w.BitmapLen() == size
}

func checkConcurrent() bool {
	const (
		goroutines = 16
		seqs       = 1000
	)
	w := seqwin.New(seqs)
	counts := make([]int32, seqs+1)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for seq := 1; seq <= seqs; seq++ {
				if w.Accept(uint64(seq)) == seqwin.Fresh {
					counts[seq]++
				}
			}
		}()
	}
	close(start)
	wg.Wait()

	for seq := 1; seq <= seqs; seq++ {
		if counts[seq] != 1 {
			return false
		}
	}
	return w.Highest() == seqs
}
