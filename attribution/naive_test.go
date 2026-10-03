package attribution

import (
	"errors"
	"math/rand"
	"sort"
	"testing"
)

// naiveSim 是与生产实现相互独立、按题面逐事件直写的朴素模拟器。
type naiveSim struct {
	cfg Config
	p   map[[2]string]*naivePair
	u   map[string]int64
	has map[string]bool
	s   Stats
}

type naivePair struct {
	wm         int64
	hasWM      bool
	anchorT    int64
	hasAnchor  bool
	anchorUsed bool
	e          int64
	hasE       bool
}

func newNaive(cfg Config) *naiveSim {
	return &naiveSim{
		cfg: cfg,
		p:   map[[2]string]*naivePair{},
		u:   map[string]int64{},
		has: map[string]bool{},
	}
}

func (n *naiveSim) pair(user, ad string) *naivePair {
	k := [2]string{user, ad}
	st := n.p[k]
	if st == nil {
		st = &naivePair{}
		n.p[k] = st
	}
	return st
}

// submit 按题面规则处理一批；乱序时返回 false 且回滚全部状态。
func (n *naiveSim) submit(events []Event) ([]Outcome, bool) {
	if err := validateEvents(events); err != nil {
		return nil, false
	}
	savedPairs := map[[2]string]naivePair{}
	savedU := map[string]int64{}
	savedHas := map[string]bool{}
	for k, v := range n.p {
		savedPairs[k] = *v
	}
	for k, v := range n.u {
		savedU[k] = v
	}
	for k, v := range n.has {
		savedHas[k] = v
	}
	savedStats := n.s

	order := make([]int, len(events))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := events[order[i]], events[order[j]]
		if a.T != b.T {
			return a.T < b.T
		}
		return a.Kind == Impression && b.Kind == Click
	})

	rollback := func() {
		n.p = map[[2]string]*naivePair{}
		for k, v := range savedPairs {
			cp := v
			n.p[k] = &cp
		}
		n.u = savedU
		n.has = savedHas
		n.s = savedStats
	}

	results := make([]Outcome, len(events))

	// 乱序校验先于任何状态变更：每对在批内维护推演水位，并与已提交水位比较。
	wm := map[[2]string]int64{}
	wmHas := map[[2]string]bool{}
	for _, idx := range order {
		e := events[idx]
		key := [2]string{e.User, e.Ad}
		if !wmHas[key] {
			if st := n.p[key]; st != nil && st.hasWM {
				wm[key] = st.wm
			}
			wmHas[key] = true
		}
		if e.T < wm[key] {
			rollback()
			return nil, false
		}
		wm[key] = e.T
	}

	bump := func(o Outcome) {
		switch o {
		case Counted:
			n.s.Counted++
		case Cooldown:
			n.s.Cooldown++
		case NotVisible:
			n.s.NotVisible++
		case Attributed:
			n.s.Attributed++
		case Duplicate:
			n.s.Duplicate++
		case Expired:
			n.s.Expired++
		case TooFast:
			n.s.TooFast++
		case NoImpression:
			n.s.NoImpression++
		case CrossTalk:
			n.s.CrossTalk++
		}
	}

	for _, idx := range order {
		e := events[idx]
		st := n.pair(e.User, e.Ad)
		var o Outcome
		if e.Kind == Impression {
			switch {
			case e.V < n.cfg.V:
				o = NotVisible
			case st.hasAnchor && e.T-st.anchorT < n.cfg.C:
				o = Cooldown
			case st.hasE && e.T < st.e:
				o = Cooldown
			default:
				o = Counted
				st.anchorT = e.T
				st.hasAnchor = true
				st.anchorUsed = false
			}
		} else {
			switch {
			case !st.hasAnchor:
				o = NoImpression
			case e.T-st.anchorT > n.cfg.A:
				o = Expired
			case e.T-st.anchorT < n.cfg.Tmin:
				o = TooFast
			case st.anchorUsed:
				o = Duplicate
			case n.cfg.Tu > 0 && n.has[e.User] && e.T-n.u[e.User] < n.cfg.Tu:
				o = CrossTalk
			default:
				o = Attributed
				st.anchorUsed = true
				st.e = e.T + n.cfg.Lk
				st.hasE = true
				if !n.has[e.User] || e.T > n.u[e.User] {
					n.u[e.User] = e.T
					n.has[e.User] = true
				}
			}
		}
		st.wm = e.T
		st.hasWM = true
		bump(o)
		results[idx] = o
	}
	return results, true
}

var _ = newNaive

// TestRandomAgainstNaive：2000 组随机批序列，生产实现与朴素模拟器逐事件对照。
func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	for iter := 0; iter < 2000; iter++ {
		cfg := Config{
			V:    int64(rng.Intn(6)),
			C:    int64(rng.Intn(6)),
			A:    int64(rng.Intn(20)),
			Tmin: int64(rng.Intn(4)),
			Lk:   int64(rng.Intn(6)),
			Tu:   int64(rng.Intn(8)),
		}
		real, err := New(cfg)
		if err != nil {
			t.Fatalf("iter %d: 构造失败：%v", iter, err)
		}
		sim := newNaive(cfg)

		numBatches := 1 + rng.Intn(6)
		users := []string{"u0", "u1", "u2"}
		ads := []string{"a0", "a1", "a2"}
		for b := 0; b < numBatches; b++ {
			n := 1 + rng.Intn(14)
			events := make([]Event, n)
			base := int64(rng.Intn(3) - 1) // 偶尔制造跨批回退事件
			for i := range events {
				user := users[rng.Intn(len(users))]
				ad := ads[rng.Intn(len(ads))]
				ts := int64(rng.Intn(40)) + base
				if ts < 0 {
					ts = 0
				}
				if rng.Intn(30) == 0 {
					// 偶发非法事件（空字段或越界），整批必须被双方一致拒绝。
					events[i] = Event{Kind: Impression, User: "", Ad: ad, T: ts, V: int64(rng.Intn(8))}
				} else if rng.Intn(2) == 0 {
					events[i] = NewImpression(user, ad, ts, int64(rng.Intn(8)))
				} else {
					events[i] = NewClick(user, ad, ts)
				}
			}

			got, gErr := real.Submit(events)
			want, ok := sim.submit(events)
			if (gErr != nil) != !ok {
				t.Fatalf("iter %d batch %d: 拒绝行为不一致 real=%v simOK=%v\n输入=%v",
					iter, b, gErr, ok, events)
			}
			if gErr != nil {
				if !errors.Is(gErr, ErrOutOfOrder) && !errors.Is(gErr, ErrInvalid) {
					t.Fatalf("iter %d batch %d: 非预期错误 %v", iter, b, gErr)
				}
				if real.Stats() != sim.s {
					t.Fatalf("iter %d batch %d: 拒绝后计数不一致 real=%+v sim=%+v",
						iter, b, real.Stats(), sim.s)
				}
				continue
			}
			if len(got) != len(want) {
				t.Fatalf("iter %d batch %d: 结论数量不一致", iter, b)
			}
			for i := range got {
				if got[i].Outcome != want[i] {
					logResults(t, events, got, nil)
					t.Fatalf("iter %d batch %d 事件 %d: got %s want %s (cfg=%+v)",
						iter, b, i, got[i].Outcome, want[i], cfg)
				}
			}
			if iter < 25 {
				logResults(t, events, got, nil)
			}
			if real.Stats() != sim.s {
				t.Fatalf("iter %d batch %d: 计数不一致 real=%+v sim=%+v",
					iter, b, real.Stats(), sim.s)
			}
		}

	}
}

// TestReplayDeterminism：相同批序列重放得到完全相同的结论与计数。
func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(777))
	build := func() (*Reconciler, [][]Event, [][]Result) {
		cfg := Config{V: 3, C: 4, A: 15, Tmin: 2, Lk: 5, Tu: 6}
		r, _ := New(cfg)
		var batches [][]Event
		var all [][]Result
		for b := 0; b < 30; b++ {
			n := 1 + rng.Intn(10)
			ev := make([]Event, n)
			for i := range ev {
				ts := int64(b*20 + rng.Intn(22))
				u := "u" + string(rune('0'+rng.Intn(3)))
				a := "a" + string(rune('0'+rng.Intn(3)))
				if rng.Intn(2) == 0 {
					ev[i] = NewImpression(u, a, ts, int64(rng.Intn(6)))
				} else {
					ev[i] = NewClick(u, a, ts)
				}
			}
			res, err := r.Submit(ev)
			if err != nil {
				t.Fatalf("重放序列构造出错：%v", err)
			}
			batches = append(batches, ev)
			all = append(all, res)
		}
		return r, batches, all
	}
	r1, batches, first := build()
	r2, _ := New(Config{V: 3, C: 4, A: 15, Tmin: 2, Lk: 5, Tu: 6})
	for b, ev := range batches {
		res, err := r2.Submit(ev)
		if err != nil {
			t.Fatal(err)
		}
		for i := range res {
			if res[i].Outcome != first[b][i].Outcome {
				t.Fatalf("重放不确定 batch %d 事件 %d：%s vs %s",
					b, i, res[i].Outcome, first[b][i].Outcome)
			}
		}
	}
	if r1.Stats() != r2.Stats() {
		t.Fatalf("重放计数不一致：%+v vs %+v", r1.Stats(), r2.Stats())
	}
}
