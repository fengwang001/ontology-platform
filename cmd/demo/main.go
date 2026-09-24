package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/api"
)

func ok(b bool) string {
	if b {
		return "OK"
	}
	return "FAIL"
}
func dump(k *api.KV) string {
	s := "{"
	for _, key := range []string{"a", "b", "c"} {
		if v, has := k.Get(key); has {
			s += fmt.Sprintf("%s%d", key, v)
		}
	}
	return s + "}"
}

// trace runs the twelve prescribed ops, returning each post-step store and the
// step 6/8/12 verdicts plus a partial-rollback flag (step 8 exactly).
func trace() (stores string, verdict, partial, releaseUnchanged bool) {
	k, _ := api.New(100)
	add := func() { stores += " " + dump(k) }
	k.Set("a", 1)
	add()
	p0 := k.Savepoint()
	add()
	k.Set("a", 2)
	add()
	p1 := k.Savepoint()
	add()
	k.Set("b", 5)
	add()
	before := dump(k)
	releaseOK := k.Release(p1) == nil
	releaseUnchanged = releaseOK && dump(k) == before
	add()
	k.Set("c", 8)
	add()
	rollErr := k.RollbackTo(p0)
	partial = rollErr == nil && dump(k) == "{a1}"
	add()
	k.Set("b", 3)
	add()
	p2 := k.Savepoint()
	add()
	k.Set("c", 6)
	add()
	deadRejected := errors.Is(k.RollbackTo(p1), api.ErrSavepoint) && dump(k) == "{a1b3c6}"
	add()
	_ = p2
	verdict = releaseOK && partial && deadRejected
	return
}

// errorChecks verifies the four distinct sentinels, no-trace-on-failure and
// that the store stays usable afterwards.
func errorChecks() (four, notrace bool) {
	_, e0 := api.New(0)
	k, _ := api.New(1)
	e1 := k.Set("", 1)
	k.Set("a", 1)
	e2 := k.Set("b", 2)
	e3, e4 := k.RollbackTo(99), k.Release(99)
	four = errors.Is(e0, api.ErrInvalidLimit) && errors.Is(e1, api.ErrEmptyKey) &&
		errors.Is(e2, api.ErrLogFull) && errors.Is(e3, api.ErrSavepoint) && errors.Is(e4, api.ErrSavepoint)
	v, hasA := k.Get("a")
	_, hasB := k.Get("b")
	id := k.Savepoint()
	k.Release(id)
	notrace = hasA && v == 1 && !hasB && errors.Is(k.RollbackTo(id), api.ErrSavepoint)
	return
}

func concurrentGet() bool {
	k, _ := api.New(100)
	want := map[string]int{}
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("k%d", i)
		k.Set(key, i)
		want[key] = i
	}
	const n = 8
	start, wg := make(chan struct{}), sync.WaitGroup{}
	bad := false
	var mu sync.Mutex
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for key, v := range want {
				if gv, has := k.Get(key); !has || gv != v {
					mu.Lock()
					bad = true
					mu.Unlock()
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	return !bad
}

func main() {
	stores, verdict, partial, releaseUnchanged := trace()
	fmt.Println("twelve-step stores:" + stores)
	fmt.Println("6/8/12 verdicts (release/partial/dead):", ok(verdict))
	fmt.Println("partial rollback exact:", ok(partial))
	k, _ := api.New(100)
	fmt.Println("naive replay equivalence (SelfCheck):", ok(k.SelfCheck()))
	fmt.Println("release leaves store unchanged:", ok(releaseUnchanged))
	four, notrace := errorChecks()
	fmt.Println("four distinct sentinel errors:", ok(four))
	fmt.Println("rejected leaves no trace, still usable:", ok(notrace))
	fmt.Println("O(1) locate m=100..10000:", ok(k.CheckLocateO1()))
	fmt.Println("concurrent Get identical:", ok(concurrentGet()))
}
