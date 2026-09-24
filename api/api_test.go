package api

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/src"
)

// 不变量 1：随机交错后，队列清空、令牌完结，Get 与无缓存参照一致。
func TestInvariantReference(t *testing.T) {
	if err := New(8).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func drive(a *System, rng *rand.Rand, n int, after func(string)) {
	ops := []func(string){
		func(k string) { _ = a.Update(k, "v") }, func(k string) { _ = a.Delete(k) },
		func(k string) { _ = a.Deliver(rng.Intn(3)) }, func(k string) { _, _, _ = a.Get(k) },
	}
	for i := 0; i < n; i++ {
		k := string(rune('a' + rng.Intn(6)))
		ops[rng.Intn(4)](k)
		after(k)
	}
}

// 不变量 2：fence 单调不减；任意时刻条目版本 >= fence。
func TestInvariantFence(t *testing.T) {
	a := New(16)
	prev := map[string]int64{}
	drive(a, rand.New(rand.NewSource(1)), 300, func(k string) {
		f := a.Fence(k)
		if f < prev[k] {
			t.Fatalf("fence %s regressed: %d < %d", k, f, prev[k])
		}
		prev[k] = f
		if _, ver, _, has := a.Snapshot(k); has && ver < f {
			t.Fatalf("entry %s@%d below fence %d", k, ver, f)
		}
	})
}

// 不变量 3：同一 Key 的条目版本单调不减（含删除后重写）。
func TestInvariantNoRegression(t *testing.T) {
	a := New(16)
	last := map[string]int64{}
	drive(a, rand.New(rand.NewSource(2)), 300, func(k string) {
		if _, ver, _, has := a.Snapshot(k); has {
			if ver < last[k] {
				t.Fatalf("entry %s regressed: %d < %d", k, ver, last[k])
			}
			last[k] = ver
		}
	})
}

// 不变量 4：被拒操作（四类错误）不改变任何状态；错误失败的 FinishRead 不作废令牌。
func TestFailureAtomicity(t *testing.T) {
	a := New(1)
	_ = a.Update("k", "v")
	_ = a.Deliver(1)
	_ = a.Update("j", "1")
	tk, _ := a.BeginRead("k")
	_, _ = a.FinishRead(tk)
	state := func() string {
		_, ver, x, has := a.Snapshot("k")
		return fmt.Sprint(a.SourceReads(), a.Pending(), a.Fence("k"), ver, x, has)
	}
	fin := func(t src.Token) error { _, e := a.FinishRead(t); return e }
	get := func(k string) error { _, _, e := a.Get(k); return e }
	ops := []func() error{
		func() error { return a.Update("", "x") }, func() error { return get("") },
		func() error { return a.Delete("ghost") }, func() error { return fin(tk) },
		func() error { return fin(src.Token{ID: 9}) },
		func() error { return a.Deliver(1) }, func() error { return get("j") },
	}
	wants := []error{ErrEmptyKey, ErrEmptyKey, ErrNotExist, ErrBadToken, ErrBadToken, ErrTooManyKeys, ErrTooManyKeys}
	for i, op := range ops {
		before := state()
		if err := op(); !errors.Is(err, wants[i]) {
			t.Errorf("op %d: got %v, want %v", i, err, wants[i])
		}
		if state() != before {
			t.Errorf("op %d changed state", i)
		}
	}
	b := New(1)
	_, _, _ = b.Get("x")
	tk3, _ := b.BeginRead("y")
	_, e1 := b.FinishRead(tk3)
	_, e2 := b.FinishRead(tk3)
	if !errors.Is(e1, ErrTooManyKeys) || !errors.Is(e2, ErrTooManyKeys) {
		t.Fatal("failed FinishRead must not consume the token")
	}
}

// 第三节：十四步的 fence / 缓存条目 / 回填结果逐步核验。
func TestFourteenSteps(t *testing.T) {
	a := New(4)
	var r1, r2, r3 src.Token
	var acc6, acc7, acc12, x13, x14 bool
	var g13, g14 string
	steps := []func(){
		func() { _ = a.Update("k", "a") }, func() { _ = a.Deliver(9) }, func() { r1, _ = a.BeginRead("k") },
		func() { _ = a.Update("k", "b") }, func() { r2, _ = a.BeginRead("k") }, func() { acc6, _ = a.FinishRead(r2) },
		func() { acc7, _ = a.FinishRead(r1) }, func() { _ = a.Deliver(9) }, func() { r3, _ = a.BeginRead("k") },
		func() { _ = a.Delete("k") }, func() { _ = a.Deliver(9) }, func() { acc12, _ = a.FinishRead(r3) },
		func() { g13, x13, _ = a.Get("k") }, func() { g14, x14, _ = a.Get("k") },
	}
	tr := ""
	for _, s := range steps {
		s()
		_, ver, x, has := a.Snapshot("k")
		e := "-"
		if has && x {
			e = fmt.Sprintf("v@%d", ver)
		} else if has {
			e = fmt.Sprintf("n@%d", ver)
		}
		tr += fmt.Sprintf("%d/%s;", a.Fence("k"), e)
	}
	want := "0/-;1/-;1/-;1/-;1/-;1/v@2;1/v@2;2/v@2;2/v@2;2/v@2;3/-;3/-;3/n@3;3/n@3;"
	if tr != want {
		t.Fatalf("trace:\n got %s\nwant %s", tr, want)
	}
	if !acc6 || acc7 || acc12 {
		t.Fatalf("backfill accept(6,7,12) = %v,%v,%v", acc6, acc7, acc12)
	}
	if g13 != "" || x13 || g14 != "" || x14 || a.SourceReads() != 4 {
		t.Fatalf("step13/14: %q/%v %q/%v reads=%d", g13, x13, g14, x14, a.SourceReads())
	}
}

// 并发：写、投递、读、延迟回填同时发生；结束后无永久陈旧。
func TestConcurrency(t *testing.T) {
	a := New(16)
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id)))
			var pend []src.Token
			ops := []func(string){
				func(k string) { _ = a.Update(k, "v") }, func(k string) { _ = a.Delete(k) },
				func(k string) { _ = a.Deliver(rng.Intn(3)) }, func(k string) { _, _, _ = a.Get(k) },
				func(k string) { t, _ := a.BeginRead(k); pend = append(pend, t) },
			}
			for i := 0; i < 300; i++ {
				ops[rng.Intn(5)](string(rune('a' + rng.Intn(6))))
				if len(pend) > 2 {
					_, _ = a.FinishRead(pend[0])
					pend = pend[1:]
				}
			}
			for _, t := range pend {
				_, _ = a.FinishRead(t)
			}
		}(w)
	}
	wg.Wait()
	for a.Pending() > 0 {
		_ = a.Deliver(a.Pending())
	}
	for i := 0; i < 6; i++ {
		k := string(rune('a' + i))
		v1, e1, _ := a.Get(k)
		v2, e2 := a.SourceState(k)
		if v1 != v2 || e1 != e2 {
			t.Fatalf("key %s: cache (%q,%v) != source (%q,%v)", k, v1, e1, v2, e2)
		}
	}
}
