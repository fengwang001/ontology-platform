package blocksched_test

// naiveScheduler 是把题目规则逐条直译的朴素参考实现。
// 不考虑性能，只求与文字规则一一对应，用于和正式实现做差分对照。

import "sort"

type naivePeer struct {
	id       string
	have     map[int]bool
	timeouts int
	fails    map[int]bool
	inflight map[int]int64 // block -> issued
	banned   bool
}

type naive struct {
	b, k, g, m, f int
	t             int64
	now           int64
	peers         map[string]*naivePeer
	banned        map[string]bool
	complete      map[int]bool
}

func newNaive(cfg testCfg) *naive {
	n := &naive{
		b: cfg.B, k: cfg.K, g: cfg.G, m: cfg.M, f: cfg.F, t: cfg.T,
		peers:    map[string]*naivePeer{},
		banned:   map[string]bool{},
		complete: map[int]bool{},
	}
	return n
}

type testCfg struct {
	B, K, G, M, F int
	T             int64
}

func (n *naive) avail(b int) int {
	cnt := 0
	for _, p := range n.peers {
		if !p.banned && p.have[b] {
			cnt++
		}
	}
	return cnt
}

func (n *naive) flight(b int) int {
	cnt := 0
	for _, p := range n.peers {
		if _, ok := p.inflight[b]; ok {
			cnt++
		}
	}
	return cnt
}

func (n *naive) totalFlight() int {
	cnt := 0
	for _, p := range n.peers {
		cnt += len(p.inflight)
	}
	return cnt
}

func (n *naive) sortedPeers() []*naivePeer {
	ids := make([]string, 0, len(n.peers))
	for id := range n.peers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]*naivePeer, 0, len(ids))
	for _, id := range ids {
		out = append(out, n.peers[id])
	}
	return out
}

func (n *naive) addPeer(id string, have []bool) error {
	if id == "" || len(have) != n.b {
		return errBadArg
	}
	if _, ok := n.peers[id]; ok {
		return errPeerExists
	}
	if n.banned[id] {
		return errBanned
	}
	p := &naivePeer{
		id: id, have: map[int]bool{},
		fails: map[int]bool{}, inflight: map[int]int64{},
	}
	for b, v := range have {
		p.have[b] = v
	}
	n.peers[id] = p
	return nil
}

func (n *naive) have(id string, b int) error {
	p, ok := n.peers[id]
	if !ok {
		return errNoPeer
	}
	if b < 0 || b >= n.b {
		return errBadArg
	}
	p.have[b] = true
	return nil
}

func (n *naive) drop(id string) error {
	p, ok := n.peers[id]
	if !ok {
		return errNoPeer
	}
	p.inflight = map[int]int64{}
	if p.banned {
		n.banned[id] = true
	}
	delete(n.peers, id)
	return nil
}

func (n *naive) capOf(p *naivePeer) int {
	c := n.k - p.timeouts/2
	if c < 1 {
		c = 1
	}
	return c
}

func (n *naive) allComplete() bool {
	for b := 0; b < n.b; b++ {
		if !n.complete[b] {
			return false
		}
	}
	return true
}

// naiveResult 用统一形式描述 Next 的结果，便于比较。
type naiveResult struct {
	block int
	ok    bool
	err   error
}

func (n *naive) next(now int64, id string) naiveResult {
	if now < n.now {
		return naiveResult{err: errClock}
	}
	p, ok := n.peers[id]
	if !ok {
		return naiveResult{err: errNoPeer}
	}
	if p.banned {
		return naiveResult{err: errBanned}
	}
	n.now = now

	if n.allComplete() {
		return naiveResult{}
	}
	if len(p.inflight) >= n.capOf(p) || n.totalFlight() >= n.g {
		return naiveResult{}
	}

	anyNew := false
	for b := 0; b < n.b; b++ {
		if !n.complete[b] && n.flight(b) == 0 && n.avail(b) > 0 {
			anyNew = true
			break
		}
	}

	if anyNew {
		type cand struct{ b, a int }
		var cands []cand
		for b := 0; b < n.b; b++ {
			if n.complete[b] || !p.have[b] || n.flight(b) != 0 || p.fails[b] {
				continue
			}
			if a := n.avail(b); a > 0 {
				cands = append(cands, cand{b, a})
			}
		}
		if len(cands) == 0 {
			return naiveResult{}
		}
		sort.Slice(cands, func(i, j int) bool {
			if cands[i].a != cands[j].a {
				return cands[i].a < cands[j].a
			}
			return cands[i].b < cands[j].b
		})
		b := cands[0].b
		p.inflight[b] = now
		return naiveResult{block: b, ok: true}
	}

	type cand struct{ b, fl, a int }
	var cands []cand
	for b := 0; b < n.b; b++ {
		if n.complete[b] || !p.have[b] || p.fails[b] {
			continue
		}
		if _, mine := p.inflight[b]; mine {
			continue
		}
		fl := n.flight(b)
		if fl >= n.m {
			continue
		}
		cands = append(cands, cand{b, fl, n.avail(b)})
	}
	if len(cands) == 0 {
		return naiveResult{}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].fl != cands[j].fl {
			return cands[i].fl < cands[j].fl
		}
		if cands[i].a != cands[j].a {
			return cands[i].a < cands[j].a
		}
		return cands[i].b < cands[j].b
	})
	b := cands[0].b
	p.inflight[b] = now
	return naiveResult{block: b, ok: true}
}

type naiveDone struct {
	canceled []string
	banned   bool
	err      error
}

func (n *naive) done(now int64, id string, b int, success bool) naiveDone {
	if now < n.now {
		return naiveDone{err: errClock}
	}
	p, ok := n.peers[id]
	if !ok {
		return naiveDone{err: errNoPeer}
	}
	if b < 0 || b >= n.b {
		return naiveDone{err: errBadArg}
	}
	if _, inflight := p.inflight[b]; !inflight {
		return naiveDone{err: errNoRequest}
	}
	n.now = now
	delete(p.inflight, b)

	if success {
		n.complete[b] = true
		var canceled []string
		for _, other := range n.sortedPeers() {
			if other.id == id {
				continue
			}
			if _, ok := other.inflight[b]; ok {
				canceled = append(canceled, other.id)
				delete(other.inflight, b)
			}
		}
		p.timeouts = 0
		return naiveDone{canceled: canceled}
	}

	p.fails[b] = true
	if len(p.fails) >= n.f {
		p.banned = true
		n.banned[id] = true
		p.inflight = map[int]int64{}
		return naiveDone{banned: true}
	}
	return naiveDone{}
}

type naiveTick struct {
	expired []timeoutTriple
	err     error
}

type timeoutTriple struct {
	issued int64
	id     string
	b      int
}

func (n *naive) tick(now int64) naiveTick {
	if now < n.now {
		return naiveTick{err: errClock}
	}
	n.now = now
	var expired []timeoutTriple
	for _, p := range n.sortedPeers() {
		bs := make([]int, 0)
		for b := range p.inflight {
			bs = append(bs, b)
		}
		sort.Ints(bs)
		for _, b := range bs {
			issued := p.inflight[b]
			if now-issued >= n.t {
				expired = append(expired, timeoutTriple{issued, p.id, b})
			}
		}
	}
	sort.Slice(expired, func(i, j int) bool {
		if expired[i].issued != expired[j].issued {
			return expired[i].issued < expired[j].issued
		}
		if expired[i].id != expired[j].id {
			return expired[i].id < expired[j].id
		}
		return expired[i].b < expired[j].b
	})
	for _, e := range expired {
		delete(n.peers[e.id].inflight, e.b)
		n.peers[e.id].timeouts++
	}
	return naiveTick{expired: expired}
}

func (n *naive) isComplete() bool { return n.allComplete() }
