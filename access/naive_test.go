package access_test

// naiveModel 是逐授权扫描的参考实现：不维护任何活动索引/计数，
// 每次判定都全量重建状态，用于与正式实现对照。

type nGrant struct {
	id      int64
	u, r    string
	start   int64
	end     int64
	depth   int
	parent  int64
	exts    int
	cause   string
	causeAt int64
}

type nReq struct {
	u, r   string
	dur    int64
	at     int64
	status string
}

type nLog struct {
	id    int64
	cause string
	at    int64
}

type naiveModel struct {
	p, lmax, cool int64
	m, e          int
	now           int64
	nextID        int64
	owners        map[string]map[string]bool
	grants        map[int64]*nGrant
	reqs          map[string]*nReq
	log           []nLog
}

func newNaive(p int64, m int, lmax int64, e int, cool int64) *naiveModel {
	return &naiveModel{p: p, m: m, lmax: lmax, e: e, cool: cool,
		owners: map[string]map[string]bool{}, grants: map[int64]*nGrant{}, reqs: map[string]*nReq{}}
}

const nMaxNow = int64(1_000_000_000_000)

func nClock(now int64) bool { return now < 0 || now > nMaxNow }

// aliveAt 逐节点沿父链扫描判定有效；end ≤ t 视为失效但不落地。
func (nm *naiveModel) aliveAt(g *nGrant, t int64) bool {
	for g != nil {
		if g.cause != "" || t < g.start || t >= g.end {
			return false
		}
		if g.parent == 0 {
			return true
		}
		g = nm.grants[g.parent]
	}
	return false
}

func (nm *naiveModel) findActive(u, r string, t int64) *nGrant {
	for _, g := range nm.grants {
		if g.u == u && g.r == r && nm.aliveAt(g, t) {
			return g
		}
	}
	return nil
}

func (nm *naiveModel) countActive(u string, t int64) int {
	c := 0
	for _, g := range nm.grants {
		if g.u == u && nm.aliveAt(g, t) {
			c++
		}
	}
	return c
}

func (nm *naiveModel) logDead(g *nGrant, cause string, at int64) {
	if g.cause != "" {
		return
	}
	g.cause = cause
	g.causeAt = at
	nm.log = append(nm.log, nLog{g.id, cause, at})
}

// cascade 先序：子按 id 升序、孙紧随其子。
func (nm *naiveModel) cascade(parent int64, at int64) {
	var kids []int64
	for _, g := range nm.grants {
		if g.parent == parent {
			kids = append(kids, g.id)
		}
	}
	for i := 1; i < len(kids); i++ {
		for j := i; j > 0 && kids[j-1] > kids[j]; j-- {
			kids[j-1], kids[j] = kids[j], kids[j-1]
		}
	}
	for _, id := range kids {
		g := nm.grants[id]
		if g.cause != "" {
			continue
		}
		nm.logDead(g, "Cascaded", at)
		nm.cascade(id, at)
	}
}

func (nm *naiveModel) sweep(t int64) {
	for {
		var next *nGrant
		for _, g := range nm.grants {
			if g.cause != "" || g.end > t {
				continue
			}
			if next == nil || g.end < next.end || g.end == next.end && g.id < next.id {
				next = g
			}
		}
		if next == nil {
			return
		}
		nm.logDead(next, "Expired", next.end)
		nm.cascade(next.id, next.end)
	}
}

func (nm *naiveModel) setOwners(r string, owners []string, now int64) string {
	if r == "" || nClock(now) {
		return "ErrInvalid"
	}
	for _, o := range owners {
		if o == "" {
			return "ErrInvalid"
		}
	}
	if now < nm.now {
		return "ErrClock"
	}
	set := map[string]bool{}
	for _, o := range owners {
		set[o] = true
	}
	nm.sweep(now)
	nm.now = now
	nm.owners[r] = set
	return "nil"
}

func (nm *naiveModel) tick(now int64) string {
	if nClock(now) {
		return "ErrInvalid"
	}
	if now < nm.now {
		return "ErrClock"
	}
	nm.sweep(now)
	nm.now = now
	return "nil"
}

func (nm *naiveModel) cooling(u, r string, now int64) bool {
	// 冷却表隐式：仅保留直接撤销记录，存在 log 中，扫描最大撤销时刻。
	var until int64 = -1
	for _, e := range nm.log {
		if e.cause != "Revoked" {
			continue
		}
		g := nm.grants[e.id]
		if g.u == u && g.r == r && e.at+nm.cool > until {
			until = e.at + nm.cool
		}
	}
	return now < until
}

func (nm *naiveModel) hasPending(u, r string, now int64) bool {
	for _, q := range nm.reqs {
		if q.status == "Pending" && q.u == u && q.r == r && now < q.at+nm.p {
			return true
		}
	}
	return false
}

func (nm *naiveModel) request(id, u, r string, dur, now int64) string {
	if id == "" || u == "" || r == "" || nClock(now) || dur < 1 || dur > nm.lmax {
		return "ErrInvalid"
	}
	if now < nm.now {
		return "ErrClock"
	}
	if _, ok := nm.reqs[id]; ok {
		return "ErrInvalid"
	}
	if nm.cooling(u, r, now) {
		return "ErrCooldown"
	}
	if nm.hasPending(u, r, now) {
		return "ErrDuplicatePending"
	}
	nm.sweep(now)
	nm.now = now
	nm.reqs[id] = &nReq{u: u, r: r, dur: dur, at: now, status: "Pending"}
	return "nil"
}

func (nm *naiveModel) approve(id, who string, now int64) (string, int64) {
	if id == "" || who == "" || nClock(now) {
		return "ErrInvalid", 0
	}
	if now < nm.now {
		return "ErrClock", 0
	}
	q, ok := nm.reqs[id]
	if !ok {
		return "ErrInvalid", 0
	}
	if q.status != "Pending" || now >= q.at+nm.p {
		return "ErrNotPending", 0
	}
	if q.u == who {
		return "ErrSelfApprove", 0
	}
	if !nm.owners[q.r][who] {
		return "ErrNotOwner", 0
	}
	if nm.findActive(q.u, q.r, now) != nil {
		return "ErrActiveGrant", 0
	}
	if nm.countActive(q.u, now) >= nm.m {
		return "ErrLimit", 0
	}
	nm.sweep(now)
	nm.now = now
	nm.nextID++
	nm.grants[nm.nextID] = &nGrant{id: nm.nextID, u: q.u, r: q.r, start: now, end: now + q.dur}
	q.status = "Approved"
	return "nil", nm.nextID
}

func (nm *naiveModel) deny(id, who string, now int64) string {
	if id == "" || who == "" || nClock(now) {
		return "ErrInvalid"
	}
	if now < nm.now {
		return "ErrClock"
	}
	q, ok := nm.reqs[id]
	if !ok {
		return "ErrInvalid"
	}
	if q.status != "Pending" || now >= q.at+nm.p {
		return "ErrNotPending"
	}
	if q.u == who {
		return "ErrSelfApprove"
	}
	if !nm.owners[q.r][who] {
		return "ErrNotOwner"
	}
	nm.sweep(now)
	nm.now = now
	q.status = "Denied"
	return "nil"
}

func (nm *naiveModel) extend(id int64, extra, now int64) string {
	if id < 1 || extra < 1 || nClock(now) {
		return "ErrInvalid"
	}
	if now < nm.now {
		return "ErrClock"
	}
	g, ok := nm.grants[id]
	if !ok || !nm.aliveAt(g, now) {
		return "ErrNoGrant"
	}
	if g.depth != 0 {
		return "ErrNotRoot"
	}
	if g.exts >= nm.e {
		return "ErrExtendLimit"
	}
	if g.end+extra-g.start > nm.lmax {
		return "ErrTooLong"
	}
	nm.sweep(now)
	nm.now = now
	g.end += extra
	g.exts++
	return "nil"
}

func (nm *naiveModel) delegate(id int64, to string, dur, now int64) (string, int64) {
	if id < 1 || to == "" || dur < 1 || nClock(now) {
		return "ErrInvalid", 0
	}
	if now < nm.now {
		return "ErrClock", 0
	}
	parent, ok := nm.grants[id]
	if !ok || !nm.aliveAt(parent, now) {
		return "ErrNoGrant", 0
	}
	if parent.depth >= 2 {
		return "ErrDepth", 0
	}
	if nm.findActive(to, parent.r, now) != nil {
		return "ErrActiveGrant", 0
	}
	if nm.countActive(to, now) >= nm.m {
		return "ErrLimit", 0
	}
	nm.sweep(now)
	nm.now = now
	end := now + dur
	if parent.end < end {
		end = parent.end
	}
	nm.nextID++
	nm.grants[nm.nextID] = &nGrant{id: nm.nextID, u: to, r: parent.r, start: now,
		end: end, depth: parent.depth + 1, parent: parent.id}
	return "nil", nm.nextID
}

func (nm *naiveModel) revoke(id int64, who string, now int64) string {
	if id < 1 || who == "" || nClock(now) {
		return "ErrInvalid"
	}
	if now < nm.now {
		return "ErrClock"
	}
	g, ok := nm.grants[id]
	if !ok || !nm.aliveAt(g, now) {
		return "ErrNoGrant"
	}
	if !nm.owners[g.r][who] {
		return "ErrNotOwner"
	}
	nm.sweep(now)
	nm.now = now
	nm.logDead(g, "Revoked", now)
	nm.cascade(id, now)
	return "nil"
}

func (nm *naiveModel) check(u, r string, now int64) bool {
	if u == "" || r == "" || nClock(now) || now < nm.now {
		return false
	}
	return nm.findActive(u, r, now) != nil
}

func (nm *naiveModel) logSig() []nLog {
	out := make([]nLog, len(nm.log))
	copy(out, nm.log)
	return out
}
