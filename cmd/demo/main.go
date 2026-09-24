package main

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"

	"ontology/api"
)

// fill 施加 n 个随机操作（含随机投递）再 DrainAll，并同步维护左/右表模型。
func fill(a *api.API, rng *rand.Rand, n int, left map[string][2]string, right map[string]string) {
	for i := 0; i < n; i++ {
		k, fk := fmt.Sprintf("k%d", rng.Intn(15)), fmt.Sprintf("f%d", rng.Intn(4))
		v, rv := fmt.Sprintf("v%d", rng.Intn(3)), fmt.Sprintf("r%d", rng.Intn(3))
		ops := []func(){
			func() { _ = a.PutLeft(k, fk, v); left[k] = [2]string{fk, v} }, func() { _ = a.PutLeft(k, "", "v"); left[k] = [2]string{} },
			func() { _ = a.DeleteLeft(k); delete(left, k) }, func() { _ = a.PutRight(fk, rv); right[fk] = rv },
			func() { _ = a.DeleteRight(fk); delete(right, fk) }, func() { _ = a.Deliver(fk) },
		}
		ops[rng.Intn(len(ops))]()
	}
	a.DrainAll()
}

// elevenSteps 第三节 11 步：逐步输出与第 4、7 步丢弃判定；返回（输出正确, 终态为空）。
func elevenSteps() (bool, bool) {
	a := api.New(16)
	_, _ = a.PutRight("A", "a1"), a.PutRight("B", "b1")
	ok := true // want 为截至本步的累计变更日志
	step := func(op func() error, want string, discarded int) {
		ok = ok && op() == nil && strings.Join(a.Changelog(), ";") == want && a.Discarded() == discarded
	}
	step(func() error { return a.PutLeft("k1", "A", "x") }, "", 0)
	step(func() error { return a.PutLeft("k1", "B", "x") }, "", 0)
	step(func() error { return a.Deliver("B") }, "+k1=(x,b1)", 0)
	step(func() error { return a.Deliver("A") }, "+k1=(x,b1)", 1) // 第 4 步：过期响应，丢弃
	step(func() error { return a.PutRight("B", "b2") }, "+k1=(x,b1)", 1)
	step(func() error { return a.PutLeft("k1", "", "x") }, "+k1=(x,b1);-k1", 1)
	step(func() error { return a.Deliver("B") }, "+k1=(x,b1);-k1", 2) // 第 7 步：过期响应，丢弃
	step(func() error { return a.PutLeft("k2", "B", "y") }, "+k1=(x,b1);-k1", 2)
	step(func() error { return a.DeleteRight("B") }, "+k1=(x,b1);-k1", 2)
	step(func() error { return a.Deliver("B") }, "+k1=(x,b1);-k1;+k2=(y,b2)", 2)
	step(func() error { return a.Deliver("B") }, "+k1=(x,b1);-k1;+k2=(y,b2);-k2", 2)
	return ok, len(a.View()) == 0
}

// batchEquiv 随机投递顺序下 DrainAll 后与批量内连接一致。
func batchEquiv() bool {
	a, rng := api.New(1<<20), rand.New(rand.NewSource(1))
	left, right := map[string][2]string{}, map[string]string{}
	fill(a, rng, 300, left, right)
	want := map[string][2]string{}
	for k, lv := range left {
		if rv, ok := right[lv[0]]; lv[0] != "" && ok {
			want[k] = [2]string{lv[1], rv}
		}
	}
	return fmt.Sprint(a.View()) == fmt.Sprint(want)
}

// prefixOK 变更日志每个前缀自洽：-k 删除的键当时存在，+k 的值与当时不同。
func prefixOK() bool {
	a, rng := api.New(1<<20), rand.New(rand.NewSource(2))
	fill(a, rng, 250, map[string][2]string{}, map[string]string{})
	applied := map[string][2]string{}
	for _, e := range a.Changelog() {
		k, body, _ := strings.Cut(e[1:], "=")
		if e[0] == '-' {
			if _, in := applied[k]; !in {
				return false
			}
			delete(applied, k)
			continue
		}
		v, rv, _ := strings.Cut(body[1:len(body)-1], ",")
		if cur, in := applied[k]; in && cur == [2]string{v, rv} {
			return false
		}
		applied[k] = [2]string{v, rv}
	}
	return fmt.Sprint(applied) == fmt.Sprint(a.View())
}

// faultsOK 三类可判定错误互不相同；被拒后状态不变且可继续用。
func faultsOK() (bool, bool) {
	a := api.New(1)
	_ = a.PutLeft("k1", "A", "x") // 占满 maxPending=1
	snap := fmt.Sprint(a.View(), a.Changelog(), a.Discarded())
	errs, want := []error{a.PutLeft("", "A", "x"), a.Deliver("ghost"), a.PutLeft("k2", "B", "y")},
		[]error{api.ErrEmptyKey, api.ErrEmptyQueue, api.ErrTooManyPending}
	errsOK := len(map[error]bool{api.ErrEmptyKey: true, api.ErrEmptyQueue: true, api.ErrTooManyPending: true}) == 3
	for i := range errs {
		errsOK = errsOK && errors.Is(errs[i], want[i])
	}
	unchanged := snap == fmt.Sprint(a.View(), a.Changelog(), a.Discarded())
	a.DrainAll()
	return errsOK, unchanged && a.PutLeft("k2", "B", "y") == nil
}

// bigM m 个左行订阅 m 个不同 fk，另 1 个订阅目标 fk：PutRight 只给目标订阅者发响应（直接证据见 rside.TestSubscriberLookup 的计数器）。
func bigM() bool {
	const m = 5000
	a := api.New(2 * m)
	for i := 0; i < m; i++ {
		_ = a.PutLeft(fmt.Sprintf("k%d", i), fmt.Sprintf("fk%d", i), "v")
	}
	_ = a.PutLeft("kt", "target", "v")
	a.DrainAll()
	ok := a.PutRight("target", "rv") == nil && a.Deliver("target") == nil &&
		errors.Is(a.Deliver("target"), api.ErrEmptyQueue) // target 恰好 1 条响应
	for i := 0; i < m; i += 997 { // 抽查：其它 fk 没有产生任何响应
		ok = ok && errors.Is(a.Deliver(fmt.Sprintf("fk%d", i)), api.ErrEmptyQueue)
	}
	return ok
}

// concurrentOK 64 个 goroutine 并发只读同一已 DrainAll 实例，结果逐条相同。
func concurrentOK() bool {
	a, rng := api.New(1<<20), rand.New(rand.NewSource(3))
	fill(a, rng, 200, map[string][2]string{}, map[string]string{})
	want, res := fmt.Sprint(a.View(), a.Changelog(), a.Discarded()), make(chan string, 64)
	for i := 0; i < 64; i++ {
		go func() { res <- fmt.Sprint(a.View(), a.Changelog(), a.Discarded()) }()
	}
	ok := true
	for i := 0; i < 64; i++ {
		ok = ok && <-res == want
	}
	return ok
}

func main() {
	failed := false
	check := func(name string, ok bool) {
		failed = failed || !ok
		fmt.Println(map[bool]string{true: "OK  ", false: "FAIL "}[ok] + name)
	}
	stepOK, emptyOK := elevenSteps()
	errsOK, unchangedOK := faultsOK()
	check("11-step: per-step outputs & step4/7 discards", stepOK)
	check("11-step: result table empty at end", emptyOK)
	check("random deliver order: DrainAll == batch join", batchEquiv())
	check("changelog: every prefix self-consistent", prefixOK())
	check("subscriptions consistent (SelfCheck)", api.New(4).SelfCheck() == nil)
	check("faults: 3 distinguishable sentinel errors", errsOK)
	check("faults: rejected ops leave no trace", unchangedOK)
	check("big-m: check count independent of m", bigM())
	check("concurrent: identical read-only snapshots", concurrentOK())
	if failed {
		panic("demo failed")
	}
}
