package store

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
)

func ok(t *testing.T, c bool, m string, a ...any) {
	if !c {
		t.Fatalf(m, a...)
	}
}
func mp(t *testing.T, s *Store, v int) { ok(t, s.Publish(v) == nil, "publish") }
func ma(t *testing.T, s *Store) *Handle {
	h, e := s.Acquire()
	ok(t, e == nil, "acquire %v", e)
	return h
}

// 不变量1：随机 Publish/Acquire/Release/Get 后，AliveCount==朴素重算（当前+被持历史版本去重）。
func TestNaiveRecompute(t *testing.T) {
	type ah struct {
		h *Handle
		v int
	}
	for it := 0; it < 30; it++ {
		rng := rand.New(rand.NewSource(int64(it + 1)))
		s := New()
		mp(t, s, 0) // 首发，之后 cur 恒 >=0
		cur, nxt := 0, 1
		var active []ah
		for st := 0; st < 120; st++ {
			switch rng.Intn(4) { // 表驱动的随机到达顺序
			case 0:
				cur, nxt = nxt, nxt+1
				mp(t, s, cur)
			case 1:
				active = append(active, ah{ma(t, s), cur})
			case 2:
				if len(active) == 0 {
					break
				}
				i := rng.Intn(len(active))
				ok(t, active[i].h.Release() == nil, "release")
				active[i] = active[len(active)-1]
				active = active[:len(active)-1]
			default:
				if len(active) == 0 {
					break
				}
				i := rng.Intn(len(active))
				g, e := active[i].h.Get()
				ok(t, e == nil && g == active[i].v, "get %d w=%d", g, active[i].v)
			}
			seen := map[int]bool{cur: true}
			for _, a := range active {
				seen[a.v] = true
			}
			ok(t, s.AliveCount() == len(seen), "it%d st%d %d!=%d", it, st, s.AliveCount(), len(seen))
		}
	}
}
func TestSharedNotReclaimed(t *testing.T) {
	s := New()
	mp(t, s, 10)
	h1, h2 := ma(t, s), ma(t, s)
	mp(t, s, 20)
	v1, x1 := h1.Get()
	v2, x2 := h2.Get()
	ok(t, s.AliveCount() == 2 && x1 == nil && v1 == 10 && x2 == nil && v2 == 10, "shared a=%d", s.AliveCount())
	ok(t, h1.Release() == nil && s.AliveCount() == 2, "still referenced")
	v2, x2 = h2.Get()
	ok(t, x2 == nil && v2 == 10, "survivor %d e=%v", v2, x2)
	ok(t, h2.Release() == nil && s.AliveCount() == 1, "both released a=%d", s.AliveCount())
	r := New()
	mp(t, r, 1)
	h := ma(t, r)
	mp(t, r, 2)
	ok(t, h.Release() == nil && r.AliveCount() == 1, "after-all a=%d", r.AliveCount())
	_, e := h.Get()
	ok(t, errors.Is(e, ErrUseAfterFree), "uaf %v", e)
}
func TestImmediateReclaim(t *testing.T) {
	s := New()
	mp(t, s, 0)
	h := ma(t, s)
	mp(t, s, 1)
	ok(t, s.AliveCount() == 2, "pre alive")
	ok(t, h.Release() == nil && s.AliveCount() == 1 && s.CurrentRefs() == 1, "immediate a=%d", s.AliveCount())
}
func TestReleaseCheckedIsO1(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} { // 多档 m：检查个数不随 m 线性增长
		s, hs := New(), make([]*Handle, 0, m)
		for i := 0; i < m; i++ {
			mp(t, s, i)
			hs = append(hs, ma(t, s))
		}
		ok(t, s.AliveCount() == m && hs[0].Release() == nil, "m=%d release", m)
		ok(t, s.lastChk <= 3 && s.AliveCount() == m-1, "m=%d checked=%d", m, s.lastChk)
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	ok(t, len(map[error]bool{ErrDoubleRelease: true, ErrUseAfterFree: true, ErrEmptyStore: true, ErrNegativeValue: true}) == 4, "distinct sentinels")
	e := New()
	_, e0 := e.Acquire()
	ok(t, errors.Is(e0, ErrEmptyStore) && errors.Is(e.Publish(-1), ErrNegativeValue) && e.AliveCount() == 0, "empty rejects")
	mp(t, e, 10)
	victim, live := ma(t, e), ma(t, e)
	mp(t, e, 20)
	cur := ma(t, e)
	ok(t, victim.Release() == nil, "first release")
	bA, bR := e.AliveCount(), e.CurrentRefs()
	ok(t, errors.Is(victim.Release(), ErrDoubleRelease) && e.AliveCount() == bA && e.CurrentRefs() == bR, "double")
	_, eg := victim.Get()
	ok(t, errors.Is(eg, ErrUseAfterFree) && e.AliveCount() == bA, "uaf")
	ok(t, errors.Is(e.Publish(-3), ErrNegativeValue) && e.AliveCount() == bA && e.CurrentRefs() == bR, "negative")
	gL, eL := live.Get()
	gC, eC := cur.Get()
	ok(t, eL == nil && gL == 10 && eC == nil && gC == 20, "after rejects %d %d", gL, gC)
	ok(t, e.Publish(30) == nil && e.CurrentRefs() == 1, "still usable")
}
func TestConcurrentAcquireRelease(t *testing.T) {
	const N = 300
	s := New()
	mp(t, s, 1)
	var wg sync.WaitGroup
	res := make(chan bool, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); h, e := s.Acquire(); res <- e == nil && h.Release() == nil }()
	}
	wg.Wait()
	close(res)
	good := true
	for g := range res {
		good = good && g
	}
	ok(t, good && s.AliveCount() == 1 && s.CurrentRefs() == 1, "conc a=%d", s.AliveCount())
}
