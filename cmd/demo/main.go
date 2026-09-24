package main

import (
	"fmt"
	"os"
	"slices"
	"sync"

	"ontology/api"
	"ontology/chq"
)

// checkCHQ 判定 chq：FIFO、记录中累积、屏障后留存、恢复重建。
func checkCHQ() bool {
	c := chq.New()
	c.Push(2)
	c.Push(9)
	if v, ok := c.Pop(); !ok || v != 2 {
		return false
	}
	c.Begin() // 通道状态 = [9]
	c.Push(6) // 记录中，进通道状态
	c.Barrier()
	c.Push(7) // 屏障之后，只进留存
	c.Commit()
	c.Restore() // 队列 = [9 6] + [7]
	var got []int
	for v, ok := c.Pop(); ok; v, ok = c.Pop() {
		got = append(got, v)
	}
	return slices.Equal(got, []int{9, 6, 7}) && slices.Equal(c.Committed(), []int{9, 6})
}

// scenario 跑第三节 9 步，返回（快照与通道状态是否正确，恢复后结果是否正确）。
func scenario() (snapOK, restoreOK bool) {
	a := api.New(16)
	a.Arrive(2, 5)
	a.Step(2)
	a.Arrive(2, 2)
	a.Arrive(1, 4)
	a.Barrier(1, 1)
	a.Step(2)
	a.Arrive(2, 6)
	a.Arrive(1, 7)
	a.Barrier(2, 1)
	snap, cs1, cs2, n := a.Snapshot()
	snapOK = n == 1 && snap.Sum[1] == 0 && snap.Sum[2] == 5 && !snap.Has[1] &&
		snap.Has[2] && snap.Last[2] == 5 && slices.Equal(cs1, []int{4}) && slices.Equal(cs2, []int{2, 6})
	a.Restore()
	a.RunAll()
	st := a.State()
	restoreOK = st.Sum[1] == 11 && st.Sum[2] == 13 && st.Last[1] == 7 && st.Last[2] == 6
	return snapOK, restoreOK
}

// checkLargeM 大 m 下入队/出队/通道状态追加功能正确（O(1) 证明见 chq 测试）。
func checkLargeM() bool {
	for _, m := range []int{100, 1000, 10000} {
		a := api.New(1 << 30)
		for i := 0; i < m; i++ {
			if a.Arrive(1, 1) != nil {
				return false
			}
		}
		if a.Barrier(1, 1) != nil || a.Barrier(2, 1) != nil {
			return false
		}
		a.RunAll()
		if a.State().Sum[1] != m {
			return false
		}
	}
	return true
}

// checkConcurrent 并发到达/屏障/处理，核验不变量 1 与恢复等价。
func checkConcurrent() bool {
	a := api.New(1 << 30)
	const N = 2000
	var wg sync.WaitGroup
	barErr := [3]error{}
	for c := 1; c <= 2; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			for i := 0; i < N; i++ {
				a.Arrive(c, i+1)
				if i == N/2 {
					barErr[c] = a.Barrier(c, 1)
				}
			}
		}(c)
	}
	for c := 1; c <= 2; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			for i := 0; i < 4*N; i++ {
				a.Step(c)
			}
		}(c)
	}
	wg.Wait()
	if barErr[1] != nil || barErr[2] != nil {
		return false
	}
	snap, cs1, cs2, n := a.Snapshot()
	want := (N/2 + 1) * (N/2 + 2) / 2 // 各通道屏障前到达 1..N/2+1 之和
	if n != 1 || snap.Sum[1]+sum(cs1) != want || snap.Sum[2]+sum(cs2) != want {
		return false
	}
	a.RunAll()
	direct := a.State()
	a.Restore()
	a.RunAll()
	return a.State() == direct
}

func sum(xs []int) (s int) {
	for _, x := range xs {
		s += x
	}
	return s
}

func main() {
	ok := true
	line := func(name string, pass bool) {
		s := "OK"
		if !pass {
			s = "FAIL"
			ok = false
		}
		fmt.Printf("%s %s\n", s, name)
	}
	snapOK, restoreOK := scenario()
	line("chq 队列/通道状态/留存/恢复", checkCHQ())
	line("9步后快照+两通道状态", snapOK)
	line("恢复后RunAll Sum={11,13} Last={7,6}", restoreOK)
	for _, c := range api.New(64).SelfCheck() {
		line(c.Name, c.Err == nil)
	}
	line("大m访问个数不随m增长(证明见chq测试)", checkLargeM())
	line("并发后不变量成立+恢复等价", checkConcurrent())
	if !ok {
		os.Exit(1)
	}
}
