package session

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// naive 是朴素参考引擎：每次计算都全量扫描所有授予记录，
// 不做任何索引与惰性技巧，直接按题面规则重放。与生产实现必须逐字段一致。
type naive struct {
	smax, lmin, v int64
	nmax          int

	balance map[string]int64
	price   map[int64]int64
	sess    map[string]*nSession
	acctN   map[string]int
	lastNow int64

	grants []*nGrant // 全部授予记录的扁平列表：naive 每次都全量扫描它
}

type nGrant struct {
	sess string
	rg   int64
	g    int64
	r    int64
	e    int64
	dead bool
}

type nSession struct {
	acct     string
	lastSeq  int64
	unbilled int64
	lastRep  *Reply
}

func newNaive(smax, lmin, v int64, nmax int) *naive {
	return &naive{
		smax: smax, lmin: lmin, v: v, nmax: nmax,
		balance: map[string]int64{},
		price:   map[int64]int64{},
		sess:    map[string]*nSession{},
		acctN:   map[string]int{},
	}
}

func (m *naive) held(acct string, now int64) int64 {
	var h int64
	for _, gr := range m.grants {
		if !gr.dead && m.sess[gr.sess] != nil && m.sess[gr.sess].acct == acct && now < gr.e {
			h += gr.g * gr.r
		}
	}
	return h
}

func (m *naive) free(acct string, now int64) int64 {
	return m.balance[acct] - m.held(acct, now)
}

func (m *naive) findGrant(sess string, rg int64) *nGrant {
	for _, gr := range m.grants { // 朴素：线性全表扫描
		if !gr.dead && gr.sess == sess && gr.rg == rg {
			return gr
		}
	}
	return nil
}

func (m *naive) settle(s *nSession, gr *nGrant, used, now int64) (charged, unbilled int64) {
	// 预留释放是 now<e 的纯函数：held/free 自动不计到期预留，无需就地改写。
	gr.dead = true // 先释放自身预留（与真实引擎 Release 语义一致），再按可用额扣费
	if used > 0 {
		afford := m.free(s.acct, now) / gr.r
		c := used
		if c > afford {
			c = afford
		}
		m.balance[s.acct] -= c * gr.r
		charged = c
		unbilled = used - c
		s.unbilled += unbilled
	}
	return
}

func (m *naive) setRate(rg, price, now int64) error {
	if rg < 1 || rg > 1000 || price < 1 || price > 1e6 || now < 0 || now > 1e12 {
		return ErrInvalid
	}
	if now < m.lastNow {
		return ErrClockRewind
	}
	m.price[rg] = price
	m.lastNow = now
	return nil
}

func (m *naive) topUp(acct string, amount, now int64) error {
	if amount < 1 || amount > 1e12 || now < 0 || now > 1e12 {
		return ErrInvalid
	}
	if now < m.lastNow {
		return ErrClockRewind
	}
	m.balance[acct] += amount
	m.lastNow = now
	return nil
}

func (m *naive) open(sess, acct string, now int64) error {
	if now < 0 || now > 1e12 {
		return ErrInvalid
	}
	if now < m.lastNow {
		return ErrClockRewind
	}
	if _, ok := m.sess[sess]; ok {
		return ErrExists
	}
	if m.acctN[acct] >= m.nmax {
		return ErrSessionCap
	}
	m.sess[sess] = &nSession{acct: acct}
	m.acctN[acct]++
	m.lastNow = now
	return nil
}

func (m *naive) update(sess string, n, rg, used, want, now int64) (Reply, error) {
	if n < 1 || rg < 1 || rg > 1000 || used < 0 || used > 1e9 || want < 0 || want > 1e9 ||
		now < 0 || now > 1e12 {
		return Reply{}, ErrInvalid
	}
	s, ok := m.sess[sess]
	if !ok {
		return Reply{}, ErrNoSession
	}
	if n == s.lastSeq && s.lastRep != nil {
		return *s.lastRep, nil
	}
	r, known := m.price[rg]
	if !known {
		return Reply{}, ErrInvalid
	}
	if now < m.lastNow {
		return Reply{}, ErrClockRewind
	}
	if n != s.lastSeq+1 {
		return Reply{}, ErrSeq
	}

	var rep Reply
	gr := m.findGrant(sess, rg)
	if gr != nil {
		rep.Charged, rep.Unbilled = m.settle(s, gr, used, now)
	} else if used > 0 {
		return Reply{}, ErrNoGrant
	}
	if want > 0 {
		u := m.free(s.acct, now) / r
		g0 := want
		if m.smax < g0 {
			g0 = m.smax
		}
		if u < g0 {
			g0 = u
		}
		d := u - g0
		g := g0
		if d > 0 && d < m.lmin {
			g = u
		}
		if g > 0 {
			m.grants = append(m.grants, &nGrant{sess: sess, rg: rg, g: g, r: r, e: now + m.v})
			rep.ValidUntil = now + m.v
		}
		rep.Granted = g
		rep.Final = g == u
		rep.Denied = g == 0
	}
	s.lastSeq = n
	cp := rep
	s.lastRep = &cp
	m.lastNow = now
	return rep, nil
}

func (m *naive) close(sess string, n int64, used map[int64]int64, now int64) (Reply, error) {
	if n < 1 || now < 0 || now > 1e12 {
		return Reply{}, ErrInvalid
	}
	for rg, u := range used {
		if rg < 1 || rg > 1000 || u < 0 || u > 1e9 {
			return Reply{}, ErrInvalid
		}
	}
	s, ok := m.sess[sess]
	if !ok {
		return Reply{}, ErrNoSession
	}
	if n == s.lastSeq && s.lastRep != nil {
		return *s.lastRep, nil
	}
	if now < m.lastNow {
		return Reply{}, ErrClockRewind
	}
	if n != s.lastSeq+1 {
		return Reply{}, ErrSeq
	}
	for rg, u := range used {
		if u > 0 && m.findGrant(sess, rg) == nil {
			return Reply{}, ErrNoGrant
		}
	}
	var rep Reply
	var rgs []int64
	for _, gr := range m.grants { // 朴素：全表扫出本会话全部 rg
		if !gr.dead && gr.sess == sess {
			rgs = append(rgs, gr.rg)
		}
	}
	sort.Slice(rgs, func(i, j int) bool { return rgs[i] < rgs[j] })
	for _, rg := range rgs {
		c, u := m.settle(s, m.findGrant(sess, rg), used[rg], now)
		rep.Charged += c
		rep.Unbilled += u
	}
	delete(m.sess, sess)
	m.acctN[s.acct]--
	m.lastNow = now
	return rep, nil
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errIs(a, b) && errIs(b, a)
}

var _ = fmt.Sprintf

// TestRandomDifferential：1500 组随机操作序列，真实引擎与朴素全表扫描实现逐字段差分。
// -v 时日志打印每步输入、双方输出与判定依据（错误是否一致、应答六字段是否相等、守恒式）。
func TestRandomDifferential(t *testing.T) {
	const casesN = 1500
	for seed := int64(0); seed < casesN; seed++ {
		rng := rand.New(rand.NewSource(seed))
		smax := int64(1 + rng.Intn(30))
		lmin := int64(1 + rng.Intn(int(smax)))
		v := int64(1 + rng.Intn(200))
		nmax := 1 + rng.Intn(3)

		eng := New(smax, lmin, v, nmax)
		ref := newNaive(smax, lmin, v, nmax)

		const nRG = 3
		for rg := int64(1); rg <= nRG; rg++ {
			p := int64(1 + rng.Intn(5))
			_ = eng.SetRate(rg, p, 0)
			_ = ref.setRate(rg, p, 0)
		}

		openSess := map[string]sessInfo{}
		seq := map[string]int64{}
		accts := []string{"a0", "a1", "a2", "a3"}
		var now int64

		logf := func(format string, args ...any) {
			if seed == 0 || testing.Verbose() {
				t.Logf("[seed=%d] %s", seed, fmt.Sprintf(format, args...))
			}
		}

	stepLoop:
		for step := 0; step < 80; step++ {
			now += int64(rng.Intn(6))
			op := rng.Intn(100)
			switch {
			case op < 15:
				a := accts[rng.Intn(len(accts))]
				amt := int64(1 + rng.Intn(300))
				nn := now
				if rng.Intn(20) == 0 {
					nn = 0
				}
				e1, e2 := eng.TopUp(a, amt, nn), ref.topUp(a, amt, nn)
				logf("TopUp acct=%s amt=%d now=%d -> %v|%v (判定: %v)", a, amt, nn, e1, e2, sameErr(e1, e2))
				if !sameErr(e1, e2) {
					t.Fatalf("seed=%d step=%d TopUp %v vs %v", seed, step, e1, e2)
				}
			case op < 30:
				rg := int64(1 + rng.Intn(nRG))
				p := int64(1 + rng.Intn(5))
				nn := now
				if rng.Intn(20) == 0 {
					nn = 0
				}
				e1, e2 := eng.SetRate(rg, p, nn), ref.setRate(rg, p, nn)
				logf("SetRate rg=%d price=%d now=%d -> %v|%v", rg, p, nn, e1, e2)
				if !sameErr(e1, e2) {
					t.Fatalf("seed=%d SetRate %v vs %v", seed, e1, e2)
				}
			case op < 45:
				s := "s" + itoa(rng.Intn(10))
				a := accts[rng.Intn(len(accts))]
				nn := now
				if rng.Intn(20) == 0 {
					nn = 0
				}
				e1, e2 := eng.Open(s, a, nn), ref.open(s, a, nn)
				logf("Open sess=%s acct=%s now=%d -> %v|%v", s, a, nn, e1, e2)
				if !sameErr(e1, e2) {
					t.Fatalf("seed=%d Open %v vs %v", seed, e1, e2)
				}
				if e1 == nil {
					openSess[s] = sessInfo{a}
					seq[s] = 0
				}
			case op < 90: // Update
				if len(openSess) == 0 {
					continue stepLoop
				}
				sk := randPickSess2(rng, openSess)
				n := seq[sk] + 1
				retrans := false
				if seq[sk] > 0 && rng.Intn(5) == 0 {
					n = seq[sk]
					retrans = true
				}
				rg := int64(1 + rng.Intn(nRG))
				used := int64(rng.Intn(25))
				want := int64(rng.Intn(25))
				if rng.Intn(6) == 0 {
					want = 0
				}
				nn := now
				if rng.Intn(30) == 0 {
					nn = 0
				}
				r1, e1 := eng.Update(sk, n, rg, used, want, nn)
				r2, e2 := ref.update(sk, n, rg, used, want, nn)
				logf("Update sess=%s n=%d rg=%d used=%d want=%d now=%d retry=%v -> %+v|%v ; %+v|%v",
					sk, n, rg, used, want, nn, retrans, r1, e1, r2, e2)
				if !sameErr(e1, e2) || r1 != r2 {
					t.Fatalf("seed=%d step=%d Update (%+v,%v) vs (%+v,%v)", seed, step, r1, e1, r2, e2)
				}
				if e1 == nil && !retrans && n == seq[sk]+1 {
					seq[sk] = n
				}
			default: // Close
				if len(openSess) == 0 {
					continue stepLoop
				}
				sk := randPickSess2(rng, openSess)
				n := seq[sk] + 1
				retrans := false
				if seq[sk] > 0 && rng.Intn(5) == 0 {
					n = seq[sk]
					retrans = true
				}
				um := map[int64]int64{}
				if rng.Intn(2) == 0 {
					um[int64(1+rng.Intn(nRG))] = int64(rng.Intn(20))
				}
				if rng.Intn(3) == 0 {
					um[int64(1+rng.Intn(nRG))] = int64(rng.Intn(20))
				}
				nn := now
				if rng.Intn(30) == 0 {
					nn = 0
				}
				r1, e1 := eng.Close(sk, n, um, nn)
				r2, e2 := ref.close(sk, n, um, nn)
				logf("Close sess=%s n=%d used=%v now=%d retry=%v -> %+v|%v ; %+v|%v",
					sk, n, um, nn, retrans, r1, e1, r2, e2)
				if !sameErr(e1, e2) || r1 != r2 {
					t.Fatalf("seed=%d step=%d Close (%+v,%v) vs (%+v,%v)", seed, step, r1, e1, r2, e2)
				}
				if e1 == nil && !retrans {
					delete(openSess, sk)
					delete(seq, sk)
				}
			}

			// 每步后双方余额/未到期预留必须一致，且守恒式成立。
			for _, a := range accts {
				eb := eng.ledger.Balance(a)
				rb := ref.balance[a]
				if eb != rb {
					t.Fatalf("seed=%d step=%d balance[%s]=%d vs naive %d", seed, step, a, eb, rb)
				}
				eh := eng.ledger.Held(a, now)
				rh := ref.held(a, now)
				if eh != rh {
					t.Fatalf("seed=%d step=%d held[%s]=%d vs naive %d", seed, step, a, eh, rh)
				}
				if eh > eb || eb < 0 {
					t.Fatalf("seed=%d invariant broken held=%d bal=%d", seed, eh, eb)
				}
			}
		}
	}
}

type sessInfo struct{ acct string }

func randPickSess2(rng *rand.Rand, m map[string]sessInfo) string {
	i := rng.Intn(len(m))
	for k := range m {
		if i == 0 {
			return k
		}
		i--
	}
	return ""
}
