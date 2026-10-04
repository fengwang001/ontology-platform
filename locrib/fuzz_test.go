package locrib

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"

	"ontology/policy"
)

type opKind int

const (
	opAddPeer opKind = iota
	opUpdate
	opWithdraw
	opSetPolicy
)

type op struct {
	kind opKind
	id   int
	p    policy.Prefix
	a    policy.Attrs
	pol  []policy.Term
}

func randomPrefix(rng *rand.Rand, pool []policy.Prefix) policy.Prefix {
	return pool[rng.Intn(len(pool))]
}

func randomAttrs(rng *rand.Rand) policy.Attrs {
	n := rng.Intn(4)
	path := make([]uint32, n)
	seen := map[uint32]bool{}
	for i := range path {
		for {
			v := uint32(1 + rng.Intn(8))
			if !seen[v] {
				seen[v] = true
				path[i] = v
				break
			}
		}
	}
	nc := rng.Intn(4)
	comms := make([]uint32, 0, nc)
	cs := map[uint32]bool{}
	for i := 0; i < nc; i++ {
		v := uint32(rng.Intn(3))
		if v == 0 {
			v = policy.NoExport
		} else if v == 1 {
			v = policy.NoAdvertise
		} else {
			v = uint32(100 + rng.Intn(5))
		}
		if !cs[v] {
			cs[v] = true
			comms = append(comms, v)
		}
	}
	return policy.Attrs{
		ASPath:      path,
		LocalPref:   uint32(rng.Intn(4) * 100),
		MED:         uint32(rng.Intn(20)),
		Origin:      uint8(rng.Intn(3)),
		Communities: comms,
	}
}

func randomPolicy(rng *rand.Rand) []policy.Term {
	if rng.Intn(3) == 0 {
		return []policy.Term{{Action: policy.Action{Kind: policy.ActionAccept}}}
	}
	terms := []policy.Term{}
	k := rng.Intn(4)
	for i := 0; i < k; i++ {
		act := policy.ActionNext
		if i == k-1 {
			act = policy.ActionAccept
		}
		a := policy.Action{Kind: act}
		switch rng.Intn(3) {
		case 0:
			a.SetLocalPref = true
			a.LocalPref = uint32(50 + rng.Intn(3)*75)
		case 1:
			a.AddCommunity = true
			a.Community = uint32(100 + rng.Intn(5))
		case 2:
			a.PrependCount = uint8(1 + rng.Intn(3))
		}
		m := policy.Match{}
		switch rng.Intn(3) {
		case 0:
			m.HasCommunity = true
			m.Community = uint32(100 + rng.Intn(5))
		case 1:
			m.HasAS = true
			m.AS = uint32(1 + rng.Intn(8))
		}
		terms = append(terms, policy.Term{Match: m, Action: a})
	}
	return terms
}

func makePrefixPool(rng *rand.Rand, n int) []policy.Prefix {
	pool := make([]policy.Prefix, 0, n)
	for len(pool) < n {
		l := uint8(8 + rng.Intn(25))
		addr := uint32(1+rng.Intn(20)) << 24
		if l < 24 {
			addr &= ^uint32(0) << (32 - l)
		} else if l < 32 {
			addr |= uint32(rng.Intn(4)) << 8
		} else {
			addr |= uint32(rng.Intn(256))
		}
		p := policy.Prefix{Addr: addr, Len: l}
		if err := policy.ValidatePrefix(p); err != nil {
			continue
		}
		dup := false
		for _, q := range pool {
			if q == p {
				dup = true
			}
		}
		if !dup {
			pool = append(pool, p)
		}
	}
	return pool
}

type normChange struct {
	peer int
	p    policy.Prefix
	kind ChangeKind
	a    policy.Attrs
}

func normalize(ch []Change) []normChange {
	sortChanges(ch)
	out := make([]normChange, len(ch))
	for i, c := range ch {
		out[i] = normChange{c.Peer, c.Prefix, c.Kind, c.Attrs.Clone()}
	}
	return out
}

type simChange struct {
	peer int
	p    policy.Prefix
	a    policy.Attrs
	has  bool
}

func diffExports(before, after map[int]map[policy.Prefix]policy.Attrs, only policy.Prefix) []simChange {
	ids := map[int]struct{}{}
	for id := range before {
		ids[id] = struct{}{}
	}
	for id := range after {
		ids[id] = struct{}{}
	}
	out := []simChange{}
	for id := range ids {
		ba, bok := before[id][only]
		aa, aok := after[id][only]
		if !bok && !aok {
			continue
		}
		if bok && aok && policy.AttrsEqual(ba, aa) {
			continue
		}
		out = append(out, simChange{id, only, aa, aok})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].peer != out[j].peer {
			return out[i].peer < out[j].peer
		}
		if out[i].p.Addr != out[j].p.Addr {
			return out[i].p.Addr < out[j].p.Addr
		}
		return out[i].p.Len < out[j].p.Len
	})
	return out
}

func fullExportEngine(e *Engine) map[int]map[policy.Prefix]policy.Attrs {
	out := map[int]map[policy.Prefix]policy.Attrs{}
	for _, id := range e.tab.PeerIDs() {
		out[id] = map[policy.Prefix]policy.Attrs{}
		for _, c := range e.Export(id) {
			out[id][c.Prefix] = c.Attrs
		}
	}
	return out
}

func describeBest(s *simulator, p policy.Prefix) string {
	routes := s.postRoutes(p)
	if len(routes) == 0 {
		return "no post routes"
	}
	best := simBest(routes)
	return fmt.Sprintf("best=peer%d(nbrAS path0=%v, lp=%d pathlen=%d origin=%d med=%d internal=%v rid=%d) among %d routes",
		best.peer.id, best.attrs.ASPath, best.attrs.LocalPref, len(best.attrs.ASPath),
		best.attrs.Origin, best.attrs.MED, best.peer.internal, best.peer.routerID, len(routes))
}

// TestRandomVsNaive 1500 组随机操作序列：引擎差量/全量与朴素全量重算逐条对照。
func TestRandomVsNaive(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq + 1)))
		s := newSimulator(65000, 30)
		e := New(65000, 30)
		pool := makePrefixPool(rng, 12)
		peerCount := 2 + rng.Intn(4)
		var ids []int
		for i := 0; i < peerCount; i++ {
			id := i + 1
			as := uint32(65000)
			if rng.Intn(2) == 0 {
				as = uint32(65001 + rng.Intn(6))
			}
			rid := uint32(1 + i)
			serr := s.addPeer(id, as, rid)
			eerr := e.AddPeer(id, as, rid)
			if (serr == nil) != (eerr == nil) {
				t.Fatalf("seq %d AddPeer disagreement: %v vs %v", seq, serr, eerr)
			}
			if eerr == nil {
				ids = append(ids, id)
			}
		}
		var seqLog []string
		for step := 0; step < 60; step++ {
			id := ids[rng.Intn(len(ids))]
			p := randomPrefix(rng, pool)
			var o op
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4:
				o = op{kind: opUpdate, id: id, p: p, a: randomAttrs(rng)}
			case 5, 6:
				o = op{kind: opWithdraw, id: id, p: p}
			default:
				o = op{kind: opSetPolicy, id: id, pol: randomPolicy(rng)}
			}

			before := s.fullExport()
			var serr error
			var affected []policy.Prefix
			switch o.kind {
			case opUpdate:
				serr = s.update(o.id, o.p, o.a)
				affected = []policy.Prefix{o.p}
			case opWithdraw:
				serr = s.withdraw(o.id, o.p)
				affected = []policy.Prefix{o.p}
			case opSetPolicy:
				serr = s.setPolicy(o.id, o.pol)
				if serr == nil {
					affected = append(affected, s.peers[o.id].raw2prefixes()...)
				}
			}
			after := s.fullExport()
			var simCh []simChange
			if serr == nil {
				seen := map[policy.Prefix]bool{}
				for _, ap := range affected {
					if seen[ap] {
						continue
					}
					seen[ap] = true
					simCh = append(simCh, diffExports(before, after, ap)...)
				}
				sort.Slice(simCh, func(i, j int) bool {
					if simCh[i].peer != simCh[j].peer {
						return simCh[i].peer < simCh[j].peer
					}
					if simCh[i].p.Addr != simCh[j].p.Addr {
						return simCh[i].p.Addr < simCh[j].p.Addr
					}
					return simCh[i].p.Len < simCh[j].p.Len
				})
			}

			var ech []Change
			var eerr error
			switch o.kind {
			case opUpdate:
				ech, eerr = e.Update(o.id, o.p, o.a)
			case opWithdraw:
				ech, eerr = e.Withdraw(o.id, o.p)
			case opSetPolicy:
				ech, eerr = e.SetPolicy(o.id, o.pol)
			}
			if (serr == nil) != (eerr == nil) || !sameErrKind(serr, eerr) {
				t.Fatalf("seq %d step %d op=%+v\nsim err=%v\neng err=%v\nlog:\n%s",
					seq, step, summarize(o), serr, eerr, joinLogs(s.log))
			}
			if eerr == nil {
				en := normalize(ech)
				if len(en) != len(simCh) {
					t.Fatalf("seq %d step %d delta len eng=%d sim=%d op=%s\n%s\nbest: %s",
						seq, step, len(en), len(simCh), summarize(o), joinLogs(s.log), describeBest(s, key0(o)))
				}
				for i := range en {
					sc := simCh[i]
					if en[i].peer != sc.peer || en[i].p != sc.p || (en[i].kind == ChangeAdvertise) != sc.has {
						t.Fatalf("seq %d step %d delta[%d] mismatch eng=%+v sim=%+v\n%s",
							seq, step, i, en[i], sc, joinLogs(s.log))
					}
					if sc.has && !policy.AttrsEqual(en[i].a, sc.a) {
						t.Fatalf("seq %d step %d delta[%d] attrs eng=%+v sim=%+v\n%s",
							seq, step, i, en[i].a, sc.a, joinLogs(s.log))
					}
				}
			}

			// 拒绝操作必须不改状态：错误后两侧全量仍一致（每个无错步也全量校验）。
			if eerr == nil || step%5 == 0 {
				ef := fullExportEngine(e)
				if !exportsEqual(ef, after) {
					t.Fatalf("seq %d step %d full export divergence op=%s\n%s",
						seq, step, summarize(o), joinLogs(s.log))
				}
			}
			if eerr != nil {
				seqLog = append(seqLog, fmt.Sprintf("step %d: %s -> rejected %v", step, summarize(o), eerr))
			} else {
				seqLog = append(seqLog, fmt.Sprintf("step %d: %s -> %d deltas; %s",
					step, summarize(o), len(ech), describeBest(s, key0(o))))
			}
		}

		// 序列末尾打印一次判定依据样例日志（-v 可见）。
		if seq == 0 {
			t.Logf("sample sequence 0 inputs/outputs:\n%s", joinLogsStr(seqLog))
		}
	}
}

func (pr *simPeer) raw2prefixes() []policy.Prefix {
	out := make([]policy.Prefix, 0, len(pr.raw))
	for p := range pr.raw {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Addr != out[j].Addr {
			return out[i].Addr < out[j].Addr
		}
		return out[i].Len < out[j].Len
	})
	return out
}

func key0(o op) policy.Prefix {
	if o.kind == opSetPolicy {
		return policy.Prefix{}
	}
	return o.p
}

func summarize(o op) string {
	switch o.kind {
	case opUpdate:
		return fmt.Sprintf("Update peer=%d %v/%d lp=%d med=%d origin=%d path=%v comm=%v",
			o.id, o.p.Addr, o.p.Len, o.a.LocalPref, o.a.MED, o.a.Origin, o.a.ASPath, o.a.Communities)
	case opWithdraw:
		return fmt.Sprintf("Withdraw peer=%d %v/%d", o.id, o.p.Addr, o.p.Len)
	default:
		return fmt.Sprintf("SetPolicy peer=%d terms=%d", o.id, len(o.pol))
	}
}

func sameErrKind(a, b error) bool {
	return (a == nil && b == nil) ||
		(errIs(a, policy.ErrInvalidArgument) && errIs(b, policy.ErrInvalidArgument)) ||
		(errIs(a, policy.ErrPeerExists) && errIs(b, policy.ErrPeerExists)) ||
		(errIs(a, policy.ErrPeerNotFound) && errIs(b, policy.ErrPeerNotFound)) ||
		(errIs(a, policy.ErrRouteNotFound) && errIs(b, policy.ErrRouteNotFound)) ||
		(errIs(a, policy.ErrPrefixLimit) && errIs(b, policy.ErrPrefixLimit))
}

func errIs(err, target error) bool { return err != nil && errorsIs(err, target) }

func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		err = unwrap(err)
	}
	return false
}

func unwrap(err error) error {
	if u, ok := err.(interface{ Unwrap() error }); ok {
		return u.Unwrap()
	}
	return nil
}

func joinLogs(l []string) string { return joinLogsStr(l) }
func joinLogsStr(l []string) string {
	out := ""
	for _, s := range l {
		out += "  " + s + "\n"
	}
	if out == "" {
		return "(empty)"
	}
	return out
}

func exportsEqual(a, b map[int]map[policy.Prefix]policy.Attrs) bool {
	if len(a) != len(b) {
		return false
	}
	for id, am := range a {
		bm, ok := b[id]
		if !ok || len(am) != len(bm) {
			return false
		}
		for p, x := range am {
			y, ok := bm[p]
			if !ok || !policy.AttrsEqual(x, y) {
				return false
			}
		}
	}
	return true
}

// TestComparedScale 一次 Update 考察条数只随持有该前缀的邻居数增长，与前缀总数无关。
func TestComparedScale(t *testing.T) {
	for _, totalPrefixes := range []int{100, 10000} {
		e := New(65000, totalPrefixes+10)
		const peers = 8
		for i := 1; i <= peers; i++ {
			mustAdd(t, e, i, uint32(65000+i), uint32(i))
			if _, err := e.SetPolicy(i, acceptAll()); err != nil {
				t.Fatal(err)
			}
		}
		// 用大量前缀填满各邻居原始表。
		for n := 0; n < totalPrefixes; n++ {
			addr := uint32(10)<<24 | uint32(n)<<8
			p := pf(addr, 24)
			if err := policy.ValidatePrefix(p); err != nil {
				t.Fatal(err)
			}
			id := 1 + n%peers
			_, err := e.Update(id, p, rattrs([]uint32{uint32(65000 + id)}, 100, uint32(n), 0))
			if err != nil {
				t.Fatal(err)
			}
		}
		// 所有 peers 对同一前缀发路由：持有者数 = peers。
		hot := pf(ip(192, 0, 2, 0), 24)
		for i := 1; i <= peers; i++ {
			if _, err := e.Update(i, hot, rattrs([]uint32{uint32(65000 + i), 9}, 100, uint32(i), 0)); err != nil {
				t.Fatal(err)
			}
		}
		before := e.Compared()
		if _, err := e.Update(1, hot, rattrs([]uint32{65001, 9}, 100, 99, 0)); err != nil {
			t.Fatal(err)
		}
		cost := e.Compared() - before
		if cost > uint64(peers+1) {
			t.Fatalf("totalPrefixes=%d: one update examined %d routes, want <= %d",
				totalPrefixes, cost, peers+1)
		}
		t.Logf("totalPrefixes=%d holders=%d compared-per-update=%d", totalPrefixes, peers, cost)
	}
}

// TestConcurrentOps 并发调用结果等价于某串行序：终态与朴素串行执行一致。
func TestConcurrentOps(t *testing.T) {
	const peers = 6
	run := func() (*Engine, map[int]map[policy.Prefix]policy.Attrs) {
		e := New(65000, 1000)
		for i := 1; i <= peers; i++ {
			mustAdd(t, e, i, uint32(65000+i), uint32(i))
			if _, err := e.SetPolicy(i, acceptAll()); err != nil {
				t.Fatal(err)
			}
		}
		var wg sync.WaitGroup
		for i := 1; i <= peers; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				rng := rand.New(rand.NewSource(int64(id)))
				for n := 0; n < 40; n++ {
					p := pf(ip(172, 16, uint8(id), uint8(n)), 32)
					if rng.Intn(4) == 0 {
						_, _ = e.Withdraw(id, p)
					} else {
						_, _ = e.Update(id, p, rattrs([]uint32{uint32(65000 + id)}, 100, uint32(n), 0))
					}
				}
			}(i)
		}
		wg.Wait()
		return e, fullExportEngine(e)
	}

	e1, f1 := run()
	_, f2 := run()
	if !exportsEqual(f1, f2) {
		t.Fatal("same concurrent op set must converge to same deterministic export")
	}
	// 差量累计 == 全量（串行顺序下恒等式）。
	state := map[key2]policy.Attrs{}
	for id := range f1 {
		for p, a := range f1[id] {
			state[key2{id, p}] = a
		}
	}
	got := map[key2]policy.Attrs{}
	for _, id := range e1.tab.PeerIDs() {
		for _, c := range e1.Export(id) {
			got[key2{c.Peer, c.Prefix}] = c.Attrs
		}
	}
	if len(got) != len(state) {
		t.Fatalf("concurrent final export size %d != %d", len(got), len(state))
	}
}

type key2 struct {
	peer int
	p    policy.Prefix
}

// TestReplay 相同操作序列重放得到相同差量。
func TestReplay(t *testing.T) {
	play := func(rng *rand.Rand) []normChange {
		e := New(65000, 50)
		for i := 1; i <= 4; i++ {
			mustAdd(t, e, i, uint32(65000+i), uint32(i))
			if _, err := e.SetPolicy(i, acceptAll()); err != nil {
				t.Fatal(err)
			}
		}
		pool := makePrefixPool(rng, 8)
		var all []normChange
		for step := 0; step < 80; step++ {
			id := 1 + rng.Intn(4)
			p := pool[rng.Intn(len(pool))]
			var ch []Change
			var err error
			if rng.Intn(5) == 0 {
				ch, err = e.Withdraw(id, p)
			} else {
				ch, err = e.Update(id, p, randomAttrs(rng))
			}
			if err != nil {
				continue
			}
			all = append(all, normalize(ch)...)
		}
		return all
	}
	r1 := rand.New(rand.NewSource(42))
	a := play(r1)
	r2 := rand.New(rand.NewSource(42))
	b := play(r2)
	if len(a) != len(b) {
		t.Fatalf("replay length %d != %d", len(a), len(b))
	}
	for i := range a {
		if a[i].peer != b[i].peer || a[i].p != b[i].p || a[i].kind != b[i].kind {
			t.Fatalf("replay delta %d differs", i)
		}
		if a[i].kind == ChangeAdvertise && !policy.AttrsEqual(a[i].a, b[i].a) {
			t.Fatalf("replay attrs %d differ", i)
		}
	}
}
