package applier

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func ev(k string, s int64) Event { return Event{Key: k, Seq: s} }
func trace(ds []Decision, key string) (s string) {
	for _, d := range ds {
		if key == "" || d.Key == key {
			s += d.Key + fmt.Sprintf("%d%s", d.Seq, []string{"", "E", "X"}[d.Kind])
		}
	}
	return
}
func refKey(f map[Event]int, ma int, k string, n int64) (s string) {
	for i := int64(1); i <= n; i++ {
		s += k + fmt.Sprintf("%d%s", i, map[bool]string{true: "X", false: "E"}[f[ev(k, i)] >= ma])
	}
	return
}
func drain(a *Applier) {
	for len(a.blocked) > 0 {
		a.Tick()
	}
}
func chk(t *testing.T, c bool, f string, a ...any) {
	if !c {
		t.Fatalf(f, a...)
	}
}
func produce(a *Applier, k string, n int64, wg *sync.WaitGroup) {
	defer wg.Done()
	for s := int64(1); s <= n; s++ {
		a.Submit(ev(k, s))
	}
}
func TestElevenSteps(t *testing.T) {
	steps := []string{"A1", "A2", "B1", "A3", "C1", "B2", "T", "B3", "A4", "T", "T"}
	want := []string{"A1E", "", "", "", "C1E", "", "A2EA3E", "", "A4E", "B1XB2E", "B3E"}
	a, _ := New(3, 8, map[Event]int{ev("A", 2): 1, ev("B", 1): 99, ev("B", 3): 1})
	for i, s := range steps {
		var ds []Decision
		if s == "T" {
			ds = a.Tick()
		} else {
			ds, _ = a.Submit(ev(s[:1], int64(s[1]-'0')))
		}
		chk(t, trace(ds, "") == want[i], "step %d: %q", i+1, trace(ds, ""))
	}
	chk(t, len(a.DeadLetters()) == 1 && a.DeadLetters()[0] == ev("B", 1), "dead letters: %v", a.DeadLetters())
}
func TestFaultIsolation(t *testing.T) {
	a, _ := New(3, 8, map[Event]int{ev("A", 1): 1})
	a.Submit(ev("A", 1))
	chk(t, func() bool { ds, _ := a.Submit(ev("C", 1)); return len(ds) == 1 && ds[0].Kind == Applied }(), "C stalled by blocked A")
}
func TestSentinelErrors(t *testing.T) {
	chk(t, !errors.Is(ErrInvalid, ErrSeqOrder) && !errors.Is(ErrSeqOrder, ErrBufferCap) && !errors.Is(ErrInvalid, ErrBufferCap), "sentinels not distinct")
	for _, c := range []struct {
		ma, mb int
		f      map[Event]int
	}{{0, 1, nil}, {1, -1, nil}, {1, 1, map[Event]int{ev("", 1): 0}}, {1, 1, map[Event]int{ev("k", 1): -1}}} {
		_, e := New(c.ma, c.mb, c.f)
		chk(t, errors.Is(e, ErrInvalid), "%v", e)
	}
}
func TestRejectionNoTrace(t *testing.T) {
	a, _ := New(3, 8, map[Event]int{ev("A", 1): 1})
	a.Submit(ev("A", 1))
	z, _ := New(1, 0, map[Event]int{ev("Z", 1): 1})
	z.Submit(ev("Z", 1))
	before := len(a.decisions) + a.bufTotal + len(a.blocked) + len(z.decisions) + z.bufTotal + len(z.blocked)
	_, e1 := a.Submit(ev("A", 1))
	_, e2 := a.Submit(ev("", 9))
	_, e3 := z.Submit(ev("Z", 2))
	chk(t, errors.Is(e1, ErrSeqOrder) && errors.Is(e2, ErrInvalid) && errors.Is(e3, ErrBufferCap), "rejection classes")
	after := len(a.decisions) + a.bufTotal + len(a.blocked) + len(z.decisions) + z.bufTotal + len(z.blocked)
	chk(t, after == before, "a rejected operation changed state")
	chk(t, func() bool { ds, _ := a.Submit(ev("C", 1)); return len(ds) == 1 && ds[0].Kind == Applied }(), "applier unusable after rejection")
}
func checkRandom(t *testing.T, seed int64) {
	rnd := rand.New(rand.NewSource(seed))
	ks := []string{"a", "b", "c", "d"}
	var cnt [4]int64
	a, _ := New(3, 500, map[Event]int{ev("a", 3): 1, ev("a", 7): 99, ev("b", 3): 1, ev("b", 7): 99, ev("c", 3): 1, ev("c", 7): 99, ev("d", 3): 1, ev("d", 7): 99})
	for i := 0; i < 200; i++ {
		j := rnd.Intn(4)
		cnt[j]++
		a.Submit(ev(ks[j], cnt[j]))
		a.Tick()
		chk(t, strings.HasPrefix(refKey(a.fails, 3, ks[j], cnt[j]), trace(a.decisions, ks[j])), "seed %d non-prefix %s", seed, ks[j])
	}
	drain(a)
	got := trace(a.decisions, "a") + trace(a.decisions, "b") + trace(a.decisions, "c") + trace(a.decisions, "d")
	want := refKey(a.fails, 3, "a", cnt[0]) + refKey(a.fails, 3, "b", cnt[1]) + refKey(a.fails, 3, "c", cnt[2]) + refKey(a.fails, 3, "d", cnt[3])
	chk(t, got == want, "seed %d final mismatch", seed)
}
func runSeeds(t *testing.T, seeds ...int64) {
	for _, s := range seeds {
		checkRandom(t, s)
	}
}
func TestInvariantPrefixOrder(t *testing.T) { runSeeds(t, 1, 2, 3) }
func TestSerialReference(t *testing.T)      { runSeeds(t, 11, 22, 33) }
func TestTickInspectedCount(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		a, _ := New(3, m+8, map[Event]int{ev("Z", 1): 1})
		for i := 0; i < m; i++ {
			a.Submit(ev(fmt.Sprintf("k%05d", i), 1))
		}
		a.Submit(ev("Z", 1))
		a.Tick()
		chk(t, a.lastTickN == 1, "m=%d inspected %d, want 1", m, a.lastTickN)
	}
}
func TestConcurrentSerial(t *testing.T) {
	const N, per = 8, 30
	ks := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	a, _ := New(3, N*per, map[Event]int{ev("a", 3): 1, ev("a", 7): 99, ev("b", 3): 1, ev("b", 7): 99, ev("c", 3): 1, ev("c", 7): 99, ev("d", 3): 1, ev("d", 7): 99, ev("e", 3): 1, ev("e", 7): 99, ev("f", 3): 1, ev("f", 7): 99, ev("g", 3): 1, ev("g", 7): 99, ev("h", 3): 1, ev("h", 7): 99})
	var stop atomic.Bool
	var twg, pwg sync.WaitGroup
	twg.Add(1)
	go func() {
		defer twg.Done()
		for !stop.Load() {
			a.Tick()
		}
	}()
	for i := 0; i < N; i++ {
		pwg.Add(1)
		go produce(a, ks[i], per, &pwg)
	}
	pwg.Wait()
	stop.Store(true)
	twg.Wait()
	drain(a)
	got := trace(a.decisions, "a") + trace(a.decisions, "b") + trace(a.decisions, "c") + trace(a.decisions, "d") + trace(a.decisions, "e") + trace(a.decisions, "f") + trace(a.decisions, "g") + trace(a.decisions, "h")
	want := refKey(a.fails, 3, "a", per) + refKey(a.fails, 3, "b", per) + refKey(a.fails, 3, "c", per) + refKey(a.fails, 3, "d", per) + refKey(a.fails, 3, "e", per) + refKey(a.fails, 3, "f", per) + refKey(a.fails, 3, "g", per) + refKey(a.fails, 3, "h", per)
	chk(t, got == want, "concurrent per-key order mismatch")
}
