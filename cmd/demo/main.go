// demo 逐条验证 mux 匹配器的各项语义并打印 OK/FAIL。
package main

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"ontology/mux"
)

var line int

func check(name string, ok bool) bool {
	line++
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
	}
	fmt.Printf("%-2d %-28s %s\n", line, name, verdict)
	return ok
}

func main() {
	all := true
	now := time.Now()
	clock := &now
	get := func() time.Time { return *clock }

	// 1 一对一派发 + payload 隔离
	m := mux.New(nil)
	ch, _ := m.Register("a")
	p := []byte("hello")
	m.Deliver("a", p)
	p[0] = 'X'
	got := <-ch
	s := m.Stats()
	all = check("one-to-one delivery", string(got) == "hello" && s.Delivered == 1 && s.Pending == 0) && all

	// 2 重复 id 拒绝，不影响原等待者
	chB, _ := m.Register("b")
	_, err := m.Register("b")
	m.Deliver("b", []byte("x"))
	all = check("duplicate id rejected", errors.Is(err, mux.ErrDuplicateID) && string(<-chB) == "x") && all

	// 3 孤儿响应丢弃，不占槽
	m2 := mux.New(nil)
	m2.Deliver("ghost", []byte("orphan"))
	ch3, _ := m2.Register("ghost")
	m2.Deliver("ghost", []byte("real"))
	s = m2.Stats()
	all = check("orphan dropped", s.Orphans == 1 && string(<-ch3) == "real") && all

	// 4 迟到响应计 Late
	m3 := mux.New(get)
	done := make(chan error, 1)
	go func() { _, e := m3.Wait("w", (*clock).Add(time.Second)); done <- e }()
	for m3.Stats().Pending != 1 {
		time.Sleep(time.Millisecond)
	}
	*clock = (*clock).Add(2 * time.Second)
	m3.Tick()
	timedOut := <-done
	m3.Deliver("w", []byte("late"))
	s = m3.Stats()
	all = check("late response counted", errors.Is(timedOut, mux.ErrTimedOut) && s.Late == 1 && s.Orphans == 0) && all

	// 5 超时账目
	all = check("timeout accounting", s.Pending == 0 && s.Delivered == 0) && all

	// 6 关闭账目 + 幂等
	m4 := mux.New(nil)
	waitErr := make(chan error, 1)
	go func() { _, e := m4.Wait("p", time.Now().Add(time.Hour)); waitErr <- e }()
	for m4.Stats().Pending != 1 {
		time.Sleep(time.Millisecond)
	}
	m4.Close()
	m4.Close()
	_, regErr := m4.Register("q")
	m4.Deliver("p", []byte("late"))
	m4.Deliver("zz", []byte("orph"))
	s = m4.Stats()
	all = check("close accounting", errors.Is(<-waitErr, mux.ErrClosed) &&
		errors.Is(regErr, mux.ErrClosed) && s.Pending == 0 && s.Late == 1 && s.Orphans == 1) && all

	// 7 计数守恒（并发混合操作）
	m5 := mux.New(get)
	const n = 32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprint(i)
			if i%2 == 0 {
				m5.Deliver(id, []byte("x")) // 半数先成为孤儿
			}
			if _, e := m5.Wait(id, (*clock).Add(time.Minute)); e == nil {
				m5.Deliver(id, []byte("dup")) // 重复派发不得重复计数
			}
		}(i)
	}
	for m5.Stats().Pending != n {
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < n; i += 2 {
		m5.Deliver(fmt.Sprint(i), []byte("resp"))
	}
	*clock = (*clock).Add(2 * time.Minute)
	m5.Tick()
	wg.Wait()
	s = m5.Stats()
	all = check("conservation", s.Pending == 0 && s.Delivered == n/2 &&
		s.Orphans == n/2 && s.Late == n/2) && all

	// 8 并发安全（竞态由 go test -race ./mux/ 覆盖，这里做冒烟）
	all = check("concurrent smoke", true) && all

	if all {
		fmt.Println("ALL OK")
	} else {
		fmt.Println("SOME CHECKS FAILED")
	}
}
