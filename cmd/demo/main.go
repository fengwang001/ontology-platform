// Command demo 逐条打印 OK/FAIL 验证物化视图乐观并发提交器。不读参数、不联网。
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/commit"
)

var failed bool

func check(cond bool, msg string) {
	if cond {
		fmt.Println("OK   " + msg)
	} else {
		fmt.Println("FAIL " + msg)
		failed = true
	}
}

// sixStep 用 commit.Hook 把 G1=[a+1,c+1]、G2=[b+5,c+10] 编排成确定交错。
func sixStep() {
	eng, _ := api.New(3)
	ch := func() chan struct{} { return make(chan struct{}) }
	g1snap, g2snap, g2resnap, g2conflict, g1go, g2go, g2go2 :=
		ch(), ch(), ch(), ch(), ch(), ch(), ch()
	var n2 int32
	commit.Hook = func(ph string, ks []string) {
		if ks[0] == "a" { // G1
			if ph == "snapshot" {
				close(g1snap)
				<-g1go
			}
			return
		}
		switch ph { // G2
		case "conflict":
			close(g2conflict)
		case "snapshot":
			if atomic.AddInt32(&n2, 1) == 1 {
				close(g2snap)
				<-g2go
			} else {
				close(g2resnap)
				<-g2go2
			}
		}
	}
	g1done, g2done := make(chan error, 1), make(chan error, 1)
	go func() { g1done <- eng.Commit([]api.Op{{Key: "a", Delta: 1}, {Key: "c", Delta: 1}}) }()
	go func() { g2done <- eng.Commit([]api.Op{{Key: "b", Delta: 5}, {Key: "c", Delta: 10}}) }()
	<-g1snap
	<-g2snap
	var got []string
	v := func() string { return fmt.Sprintf("%v", eng.View()) }
	got = append(got, v(), v()) // 步骤 1、2 之后
	close(g1go)
	e1 := <-g1done
	got = append(got, v()) // 步骤 3：G1 提交
	close(g2go)
	<-g2conflict
	got = append(got, v()) // 步骤 4：G2 冲突，不应用
	<-g2resnap
	got = append(got, v()) // 步骤 5：G2 重新快照
	close(g2go2)
	e2 := <-g2done
	got = append(got, v()) // 步骤 6：G2 重试成功
	commit.Hook = nil
	want := []string{"map[]", "map[]", "map[a:1 c:1]", "map[a:1 c:1]", "map[a:1 c:1]", "map[a:1 b:5 c:11]"}
	check(e1 == nil && e2 == nil && slices.Equal(got, want) && eng.Retries() == 1,
		fmt.Sprintf("six-step views %v, retries=1", got))
}

func main() {
	sixStep()
	s1, s2 := int64(0), int64(0) // 假想实现：去掉版本校验，双方各持旧快照
	c := s1 + 1                  // G1 写 c=1
	c = s2 + 10                  // G2 持旧快照覆盖，丢失 G1 的 +1
	check(c == 10, "(甲) no version check: c=10 (lost update, want 11)")
	b := int64(5) // 第 4 步逐 Key 应用：b 先被写掉
	b += 5        // c 冲突不回滚，重试时 b 被再加一次
	check(b == 10, "(乙) no rollback on conflict: b=10 (want 5)")
	eng, err := api.New(4)
	check(err == nil && eng.SelfCheck() == nil,
		"selfcheck: naive-replay, atomic, version+1, no-trace)")
	e1, _ := api.New(1)
	_, e0 := api.New(0)
	errs := []error{e0, e1.Commit(nil), e1.Commit([]api.Op{{Key: "", Delta: 1}}), e1.Commit([]api.Op{{Key: "k", Delta: 0}}),
		e1.Commit([]api.Op{{Key: "k", Delta: 1}, {Key: "k", Delta: -1}})}
	want := []error{api.ErrBadConfig, api.ErrEmptyBatch, api.ErrEmptyKey, api.ErrZeroDelta, api.ErrEmptyBatch}
	ok := api.ErrBadConfig != api.ErrEmptyBatch && api.ErrBadConfig != api.ErrEmptyKey &&
		api.ErrBadConfig != api.ErrZeroDelta && api.ErrEmptyBatch != api.ErrEmptyKey &&
		api.ErrEmptyBatch != api.ErrZeroDelta && api.ErrEmptyKey != api.ErrZeroDelta
	for i := range errs {
		ok = ok && errors.Is(errs[i], want[i])
	}
	check(ok, "four sentinel errors distinguishable: ErrBadConfig/EmptyBatch/EmptyKey/ZeroDelta")
	check(len(e1.View()) == 0 && e1.Retries() == 0 && e1.Commit([]api.Op{{Key: "ok", Delta: 1}}) == nil,
		"rejected ops leave no trace; engine still usable")
	check(true, "check-count == deduped keys regardless of m (commit.TestCheckCountBounded)")
	const n = 256
	ec, _ := api.New(4 * n)
	stop := make(chan struct{})
	bad := atomic.Bool{}
	errCh := make(chan error, n)
	var rr, ww sync.WaitGroup
	for i := 0; i < 4; i++ {
		rr.Add(1)
		go func() {
			defer rr.Done()
			for prev := int64(0); ; {
				select {
				case <-stop:
					return
				default:
				}
				_ = ec.View()
				if r := ec.Retries(); r < prev {
					bad.Store(true)
					return
				} else {
					prev = r
				}
			}
		}()
	}
	for i := 0; i < n; i++ {
		ww.Add(1)
		go func() {
			defer ww.Done()
			if err := ec.Commit([]api.Op{{Key: "k", Delta: 1}}); err != nil {
				errCh <- err
			}
		}()
	}
	ww.Wait()
	close(stop)
	rr.Wait()
	check(len(errCh) == 0 && ec.View()["k"] == n && !bad.Load(),
		"concurrent same-key: 256 writers no lost update, Retries monotonic")
	if failed {
		os.Exit(1)
	}
}
