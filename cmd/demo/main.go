package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/elect"
	"ontology/term"
)

var failed bool

func check(ok bool, msg string, args ...any) {
	tag := "OK  "
	if !ok {
		tag = "FAIL"
		failed = true
	}
	fmt.Printf(tag+" "+msg+"\n", args...)
}

// checkTerm 核验 term 包单节点的同意/拒绝判定。
func checkTerm() {
	b := term.New()
	ok := b.Term == 0 && b.VotedFor == -1
	ok = ok && b.RequestVote(1, 1) && b.Term == 1 && b.VotedFor == 1 // 更高任期同意
	ok = ok && !b.RequestVote(2, 0) && b.VotedFor == 1               // 过期任期拒绝且不留痕
	ok = ok && !b.RequestVote(2, 1) && b.VotedFor == 1               // 同任期已投别人，拒绝
	ok = ok && b.RequestVote(1, 1)                                   // 同任期重复投同一候选，同意
	ok = ok && b.RequestVote(2, 2) && b.Term == 2 && b.VotedFor == 2 // 更高任期改投
	check(ok, "term: grant/deny semantics")
}

// checkElect 核验 elect 包：三类哨兵错误互不相同，被拒后状态不变。
func checkElect() {
	c := elect.New(3)
	_ = c.StartElection(0)
	_, _ = c.RequestVote(1, 0, 1)
	beforeT, beforeV := c.State()
	e1 := c.StartElection(9) // 越界
	_, e2 := c.RequestVote(1, 0, -1)
	_, e3 := c.RequestVote(1, 1, 5) // 自投票
	ok := errors.Is(e1, elect.ErrNodeOutOfRange) && errors.Is(e2, elect.ErrBadTerm) &&
		errors.Is(e3, elect.ErrSelfVote) && e1 != e2 && e2 != e3 && e1 != e3
	afterT, afterV := c.State()
	ok = ok && reflect.DeepEqual(beforeT, afterT) && reflect.DeepEqual(beforeV, afterV)
	ok = ok && c.Winner() == 0 // 被拒后仍可正常使用
	check(ok, "elect: 3 distinct sentinel errors, rejected ops leave no trace")
}

// checkSixSteps 复现 NOTES.md 的六步推导，逐步核对 term/votedFor/Winner。
func checkSixSteps(a *api.API) {
	steps := []struct {
		run          func()
		terms, votes []int
		winner       int
	}{
		{func() { _ = a.StartElection(0) }, []int{1, 0, 0}, []int{0, -1, -1}, -1},
		{func() { _, _ = a.RequestVote(1, 0, 1) }, []int{1, 1, 0}, []int{0, 0, -1}, 0},
		{func() { _, _ = a.RequestVote(2, 0, 1) }, []int{1, 1, 1}, []int{0, 0, 0}, 0},
		{func() { _, _ = a.RequestVote(2, 1, 1) }, []int{1, 1, 1}, []int{0, 0, 0}, 0},
		{func() { _, _ = a.RequestVote(0, 2, 0) }, []int{1, 1, 1}, []int{0, 0, 0}, 0},
		{func() { _ = a.StartElection(1) }, []int{1, 2, 1}, []int{0, 1, 0}, 0},
	}
	for i, s := range steps {
		s.run()
		terms, votes := a.Snapshot()
		ok := reflect.DeepEqual(terms, s.terms) && reflect.DeepEqual(votes, s.votes) && a.Winner() == s.winner
		check(ok, "S%d winner=%d term=%v votedFor=%v", i+1, a.Winner(), terms, votes)
	}
}

// checkConcurrent 并发只读：N 个 goroutine 读同一实例的 Winner，结果必须一致。
func checkConcurrent(a *api.API) {
	const n = 16
	got := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				got[i] = a.Winner()
			}
		}(i)
	}
	wg.Wait()
	ok := true
	for _, w := range got {
		ok = ok && w == 0
	}
	check(ok, "winner O(1) node reads (pinned by elect tests); %d concurrent readers agree", n)
}

func main() {
	checkTerm()
	checkElect()
	a, err := api.New(3)
	check(err == nil && a.SelfCheck() == nil, "api: New(3) + SelfCheck")
	checkSixSteps(a)
	checkConcurrent(a)
	if failed {
		os.Exit(1)
	}
}
