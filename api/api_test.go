package api_test

import "errors"
import "fmt"
import "math/rand"
import "sync"
import "testing"
import "ontology/api"

func batch(ol map[string][2]string, orr map[string]string) map[string][2]string {
	m := map[string][2]string{}
	for k, r := range ol {
		if rv, ok := orr[r[0]]; r[0] != "" && ok {
			m[k] = [2]string{r[1], rv}
		}
	}
	return m
}
func do(j *api.Join, p [4]string) error {
	switch p[0] {
	case "L":
		return j.PutLeft(p[1], p[2], p[3])
	case "K":
		return j.DeleteLeft(p[1])
	case "R":
		return j.PutRight(p[1], p[2])
	case "X":
		return j.DeleteRight(p[1])
	}
	return j.Deliver(p[1])
}
func TestElevenStepScenario(t *testing.T) {
	j := api.New(1 << 20)
	ops := [][4]string{{"R", "A", "a1"}, {"R", "B", "b1"}, {"L", "k1", "A", "x"}, {"L", "k1", "B", "x"}, {"D", "B"}, {"D", "A"}, {"R", "B", "b2"}, {"L", "k1", "", "x"}, {"D", "B"}, {"L", "k2", "B", "y"}, {"X", "B"}, {"D", "B"}, {"D", "B"}}
	for i, p := range ops {
		b0, d0, e := len(j.Changelog()), j.Discarded(), "....+x.-x..+-"[i]
		do(j, p) // 序列中操作均合法；若意外出错，下面的日志/丢弃断言同样会失败
		if (len(j.Changelog())-b0 == 1) != (e == '+' || e == '-') || (j.Discarded() > d0) != (e == 'x') {
			t.Fatalf("step %d: %v", i+1, j.Changelog()[b0:])
		}
	}
	wl := []api.Change{{K: "k1", Val: "x", RVal: "b1"}, {K: "k1", Delete: true}, {K: "k2", Val: "y", RVal: "b2"}, {K: "k2", Delete: true}}
	if j.Discarded() != 2 || len(j.View()) != 0 || fmt.Sprint(j.Changelog()) != fmt.Sprint(wl) || j.SelfCheck() != nil {
		t.Fatal("11-step mismatch")
	}
}
func setupRandom(seed int64) (*api.Join, map[string][2]string, map[string]string) {
	rng, j, f4 := rand.New(rand.NewSource(seed)), api.New(1<<20), []string{"A", "B", "C", ""}
	ol, orr := map[string][2]string{}, map[string]string{}
	for i := 0; i < 200; i++ { // 键/fk 均非空、额度极大，写删不会失败，oracle 无条件同步
		k, f := fmt.Sprintf("k%d", 1+rng.Intn(5)), []string{"A", "B", "C"}[rng.Intn(3)]
		switch rng.Intn(5) {
		case 0:
			fk, v := f4[rng.Intn(4)], fmt.Sprintf("v%d", rng.Intn(3))
			j.PutLeft(k, fk, v)
			ol[k] = [2]string{fk, v}
		case 1:
			j.DeleteLeft(k)
			delete(ol, k)
		case 2:
			v := fmt.Sprintf("r%d", rng.Intn(3))
			j.PutRight(f, v)
			orr[f] = v
		case 3:
			j.DeleteRight(f)
			delete(orr, f)
		default:
			j.Deliver(f)
		}
	}
	j.DrainAll()
	return j, ol, orr
}
func checkAll(t *testing.T, seeds ...int64) {
	for _, seed := range seeds {
		j, ol, orr := setupRandom(seed)
		if fmt.Sprint(j.View()) != fmt.Sprint(batch(ol, orr))
			t.Fatalf("inv1 seed=%d", seed)
		}
		cur := map[string][2]string{} // 2 日志每个前缀自洽
		for i, c := range j.Changelog() {
			old, ok, nv := cur[c.K], false, [2]string{c.Val, c.RVal}
			_, ok = cur[c.K]
			if c.Delete && !ok || !c.Delete && ok && old == nv {
				t.Fatalf("inv2 seed=%d step %d", seed, i)
			}
			cur[c.K] = nv
			if c.Delete {
				delete(cur, c.K)
			}
		}
		if fmt.Sprint(cur) != fmt.Sprint(j.View()) {
			t.Fatalf("inv2 seed=%d", seed)
		}
		mr := map[string]string{"A": "\x00mA", "B": "\x00mB", "C": "\x00mC"} // 3 订阅一致
		for fk, m := range mr {
			j.PutRight(fk, m)
		}
		j.DrainAll()
		if fmt.Sprint(j.View()) != fmt.Sprint(batch(ol, mr)) {
			t.Fatalf("inv3 seed=%d", seed)
		}
	}
}
func TestBatchRecomputation(t *testing.T)      { checkAll(t, 1, 2, 3, 42, 99) }
func TestChangelogPrefixes(t *testing.T)       { checkAll(t, 1, 7, 77) }
func TestSubscriptionConsistency(t *testing.T) { checkAll(t, 5, 6) }
func TestSentinelErrorsAtomic(t *testing.T) {
	j, ol, orr := setupRandom(11)
	snap := fmt.Sprint(j.View(), j.Changelog(), j.Discarded())
	ws := [3]error{api.ErrEmptyKey, api.ErrEmptyKey, api.ErrEmptyQueue}
	for i, p := range [][4]string{{"L", "", "A", "v"}, {"R", "", "v"}, {"D", "ZZ"}} {
		if e := do(j, p); !errors.Is(e, ws[i]) || fmt.Sprint(j.View(), j.Changelog(), j.Discarded()) != snap {
			t.Fatalf("inv4 case %d: %v", i, e)
		}
	}
	z := api.New(2)
	z.PutLeft("k1", "A", "x")
	z.PutLeft("k2", "A", "y")
	before := fmt.Sprint(z.View(), z.Changelog(), z.Discarded())
	if e := z.PutRight("A", "a9"); !errors.Is(e, api.ErrPendingLimit) || fmt.Sprint(z.View(), z.Changelog(), z.Discarded()) != before ||
		z.Deliver("A") != nil || z.Deliver("A") != nil || z.PutRight("A", "a9") != nil ||
		fmt.Sprint(j.View()) != fmt.Sprint(batch(ol, orr)) {
		t.Fatal("atomicity or usability violated")
	}
}
func TestConcurrentReadOnlyConsistency(t *testing.T) {
	j, _, _ := setupRandom(23)
	wv, wl, wd := fmt.Sprint(j.View()), fmt.Sprint(j.Changelog()), j.Discarded()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if fmt.Sprint(j.View()) != wv || fmt.Sprint(j.Changelog()) != wl || j.Discarded() != wd || j.SelfCheck() != nil {
					t.Error("inconsistent read")
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			j.PutLeft("", "A", "x")
			j.Deliver("NONE")
		}
	}()
	wg.Wait()
}
