package ratelimit_test

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/ratelimit"
)

// 朴素模拟：严格按规格逐条写成的独立实现，与被测限流器逐步对照。
// 不调用 gcra 包，占用数每次都全表重数，不做任何回收优化。

type nTier struct {
	rank int
	t, b int64
}

type nRoute struct {
	scope string
	cost  int64
}

type naive struct {
	cap    int64
	tiers  map[string]nTier
	routes map[string]nRoute
	tat    map[string]int64
	maxNow int64
}

func newNaive(capacity int64) *naive {
	return &naive{
		cap:    capacity,
		tiers:  map[string]nTier{},
		routes: map[string]nRoute{},
		tat:    map[string]int64{},
	}
}

func (n *naive) addTier(name string, rank int, t, b int64) error {
	if name == "" || rank < 1 || rank > 1000 ||
		t < 1 || t > 1_000_000 || b < 1 || b > 1_000_000 {
		return ratelimit.ErrInvalidArgument
	}
	if _, ok := n.tiers[name]; ok {
		return ratelimit.ErrDuplicate
	}
	for _, tr := range n.tiers {
		if tr.rank == rank {
			return ratelimit.ErrDuplicate
		}
	}
	n.tiers[name] = nTier{rank: rank, t: t, b: b}
	return nil
}

func (n *naive) addRoute(path, scope string, cost int64) error {
	if path == "" || cost < 1 || cost > 1_000_000 {
		return ratelimit.ErrInvalidArgument
	}
	if _, ok := n.routes[path]; ok {
		return ratelimit.ErrDuplicate
	}
	n.routes[path] = nRoute{scope: scope, cost: cost}
	return nil
}

func nCeilSeconds(ms int64) int64 {
	if ms <= 0 {
		return 0
	}
	return (ms + 999) / 1000
}

func (n *naive) derive(scopes []string) (string, nTier, error) {
	if len(n.tiers) == 0 {
		return "", nTier{}, ratelimit.ErrNoTier
	}
	bestName, best, found := "", nTier{}, false
	for _, s := range scopes {
		if !strings.HasPrefix(s, "tier:") {
			continue
		}
		tr, ok := n.tiers[s[len("tier:"):]]
		if !ok {
			continue
		}
		if !found || tr.rank > best.rank {
			bestName, best, found = s[len("tier:"):], tr, true
		}
	}
	if found {
		return bestName, best, nil
	}
	minName, min, haveMin := "", nTier{}, false
	for name, tr := range n.tiers {
		if !haveMin || tr.rank < min.rank {
			minName, min, haveMin = name, tr, true
		}
	}
	return minName, min, nil
}

func (n *naive) allow(sub, path string, scopes []string, now int64) (ratelimit.Result, error) {
	if sub == "" || path == "" {
		return ratelimit.Result{}, ratelimit.ErrInvalidArgument
	}
	for _, s := range scopes {
		if s == "" {
			return ratelimit.Result{}, ratelimit.ErrInvalidArgument
		}
	}
	if now < 0 || now > 1_000_000_000_000_000 {
		return ratelimit.Result{}, ratelimit.ErrInvalidTime
	}
	if now < n.maxNow {
		return ratelimit.Result{}, ratelimit.ErrClockRegression
	}
	rt, ok := n.routes[path]
	if !ok {
		return ratelimit.Result{}, ratelimit.ErrRouteNotFound
	}
	tierName, tr, err := n.derive(scopes)
	if err != nil {
		return ratelimit.Result{}, err
	}
	if rt.scope != "" {
		has := false
		for _, s := range scopes {
			if s == rt.scope {
				has = true
			}
		}
		if !has {
			return ratelimit.Result{}, ratelimit.ErrForbidden
		}
	}
	if rt.cost > tr.b {
		return ratelimit.Result{}, ratelimit.ErrImpossible
	}
	tat, has := n.tat[sub]
	if !has || tat <= now {
		occupancy := int64(0)
		for _, v := range n.tat {
			if v > now {
				occupancy++
			}
		}
		if occupancy >= n.cap {
			return ratelimit.Result{}, ratelimit.ErrSubjectTableFull
		}
	}
	a := now
	if has && tat > now {
		a = tat
	}
	newTAT := a + rt.cost*tr.t
	burst := tr.b * tr.t
	if newTAT-now <= burst {
		n.tat[sub] = newTAT
		n.maxNow = now
		return ratelimit.Result{
			Allowed:   true,
			Limit:     tr.b,
			Remaining: (burst - (newTAT - now)) / tr.t,
			Reset:     nCeilSeconds(newTAT - now),
			Tier:      tierName,
		}, nil
	}
	remaining := (burst - (a - now)) / tr.t
	if remaining < 0 {
		remaining = 0
	}
	return ratelimit.Result{
		Limit:      tr.b,
		Remaining:  remaining,
		Reset:      nCeilSeconds(a - now),
		RetryAfter: nCeilSeconds(newTAT - now - burst),
		Tier:       tierName,
	}, ratelimit.ErrLimited
}

func sameErr(got, want error) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return errors.Is(got, want) && errors.Is(want, got)
}

// TestNaiveSimulation：2000 组确定性随机序列，逐操作对照限流器与朴素模拟，
// 日志打印输入、输出与判定依据。固定种子保证重放结果完全相同。
func TestNaiveSimulation(t *testing.T) {
	const sequences = 2000
	tierNames := []string{"free", "pro", "ent"}
	routePool := []nRoute{{scope: "", cost: 1}, {scope: "orders", cost: 1}, {scope: "bulk", cost: 3}}
	scopePool := []string{"orders", "bulk", "tier:free", "tier:pro", "tier:ent", "tier:platinum"}
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)))
		capacity := []int64{1, 2, 3, 5, 16}[rng.Intn(5)]
		l, err := ratelimit.New(capacity)
		if err != nil {
			t.Fatalf("seq=%d New: %v", seq, err)
		}
		n := newNaive(capacity)

		check := func(op string, gotErr, wantErr error) {
			t.Helper()
			if !sameErr(gotErr, wantErr) {
				t.Fatalf("seq=%d op=%s: err=%v, naive=%v", seq, op, gotErr, wantErr)
			}
			t.Logf("seq=%d op=%s -> err=%v", seq, op, gotErr)
		}

		// 初始登记 0..3 个等级（含 0 以触发无等级）与 1..3 条路由。
		ranks := rng.Perm(10)
		for i := 0; i < rng.Intn(4); i++ {
			name := tierNames[i]
			tt := []int64{1, 10, 100, 1000}[rng.Intn(4)]
			bb := []int64{1, 2, 5, 10}[rng.Intn(4)]
			op := fmt.Sprintf("AddTier(%s,rank=%d,T=%d,B=%d)", name, ranks[i]+1, tt, bb)
			check(op, l.AddTier(name, ranks[i]+1, tt, bb), n.addTier(name, ranks[i]+1, tt, bb))
		}
		for i := 0; i < 1+rng.Intn(3); i++ {
			rt := routePool[rng.Intn(len(routePool))]
			path := fmt.Sprintf("/r%d", i)
			op := fmt.Sprintf("AddRoute(%s,scope=%q,cost=%d)", path, rt.scope, rt.cost)
			check(op, l.AddRoute(path, rt.scope, rt.cost), n.addRoute(path, rt.scope, rt.cost))
		}

		genNow := int64(0)
		ops := 10 + rng.Intn(30)
		for i := 0; i < ops; i++ {
			switch rng.Intn(10) {
			case 0: // 随机 AddTier：可能非法或重复
				name := tierNames[rng.Intn(len(tierNames))]
				rank := rng.Intn(1002)
				tt := []int64{0, 1, 100, 1_000_000, 1_000_001}[rng.Intn(5)]
				bb := []int64{0, 1, 5, 1_000_000, 1_000_001}[rng.Intn(5)]
				op := fmt.Sprintf("AddTier(%s,rank=%d,T=%d,B=%d)", name, rank, tt, bb)
				check(op, l.AddTier(name, rank, tt, bb), n.addTier(name, rank, tt, bb))
			case 1: // 随机 AddRoute：可能非法或重复
				path := fmt.Sprintf("/r%d", rng.Intn(4))
				cost := []int64{0, 1, 3, 1_000_000, 1_000_001}[rng.Intn(5)]
				scope := []string{"", "orders", "bulk"}[rng.Intn(3)]
				op := fmt.Sprintf("AddRoute(%s,scope=%q,cost=%d)", path, scope, cost)
				check(op, l.AddRoute(path, scope, cost), n.addRoute(path, scope, cost))
			default: // Allow
				sub := []string{"u0", "u1", "u2", "u3"}[rng.Intn(4)]
				if rng.Intn(20) == 0 {
					sub = ""
				}
				path := fmt.Sprintf("/r%d", rng.Intn(4)) // 可能不存在
				var scopes []string
				for _, s := range scopePool {
					if rng.Intn(2) == 0 {
						scopes = append(scopes, s)
					}
				}
				if rng.Intn(20) == 0 {
					scopes = append(scopes, "")
				}
				switch rng.Intn(20) {
				case 0:
					genNow -= int64(rng.Intn(1000)) // 可能时钟回退
				case 1:
					genNow = 1_000_000_000_000_001 // 非法时间
				case 2:
					genNow += 1_000_000
				default:
					genNow += int64(rng.Intn(3000))
				}
				now := genNow
				op := fmt.Sprintf("Allow(sub=%q,path=%q,scopes=%v,now=%d)", sub, path, scopes, now)
				res, err := l.Allow(sub, path, scopes, now)
				nres, nerr := n.allow(sub, path, scopes, now)
				if !sameErr(err, nerr) || res != nres {
					t.Fatalf("seq=%d op=%s: got (%+v,%v), naive (%+v,%v)",
						seq, op, res, err, nres, nerr)
				}
				why := "allowed"
				if err != nil {
					why = err.Error()
				}
				t.Logf("seq=%d op=%s -> result=%+v why=%s", seq, op, res, why)
			}
		}
	}
}
