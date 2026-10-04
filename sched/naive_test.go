package sched_test

import "sort"

// naiveModel 是独立于生产代码的逐步朴素模拟：窗口两代参数显式保存，
// 指令用切片线性扫描，严格按题面次序操作，用于随机序列对照。

type nCmd struct {
	id      string
	size    int64
	prio    int
	expire  int64
	seq     int
	sent    int
	win     int64
	first   int64
	status  string // pending / inflight
	outcome string // "" / Acked / Expired / Failed
}

type nParam struct{ P, O, W int64 }

type nDev struct {
	cur       nParam
	eff       int64
	prevS     int64
	prevE     int64
	hasPrev   bool
	now       int64
	cmds      []*nCmd
	seqGen    int
	winUsed   int
	winBytes  int64
	curWin    int64
	delivered []string
	expired   []string
	failed    []string
}

type naive struct {
	K, R int
	Bw   int64
	Q    int
	devs map[string]*nDev
}

func nFirstStart(p nParam, eff int64) int64 {
	if eff <= p.O {
		return p.O
	}
	k := (eff - p.O + p.P - 1) / p.P
	return p.O + k*p.P
}

func (d *nDev) windowAt(now int64) (int64, bool) {
	if d.hasPrev && now >= d.prevS && now < d.prevE {
		return d.prevS, true
	}
	p := d.cur
	if now < p.O {
		return 0, false
	}
	s := p.O + ((now-p.O)/p.P)*p.P
	if s < d.eff || now >= s+p.W {
		return 0, false
	}
	return s, true
}

func (d *nDev) nextAvail(t int64) int64 {
	if d.hasPrev {
		if t >= d.prevS && t < d.prevE {
			return t
		}
		if t < d.prevS {
			return d.prevS
		}
	}
	if _, ok := d.windowAt(t); ok {
		return t
	}
	return nFirstStart(d.cur, t)
}

func (n *naive) alive(c *nCmd) bool { return c.outcome == "" }

// purge 返回 (expiredIds, removed)。过期次序：(expire, seq)。
func (n *naive) purge(d *nDev, now int64) []string {
	var ex []*nCmd
	kept := d.cmds[:0]
	for _, c := range d.cmds {
		if n.alive(c) && now >= c.expire {
			c.outcome = "Expired"
			ex = append(ex, c)
		} else {
			kept = append(kept, c)
		}
	}
	d.cmds = kept
	sort.Slice(ex, func(i, j int) bool {
		if ex[i].expire != ex[j].expire {
			return ex[i].expire < ex[j].expire
		}
		return ex[i].seq < ex[j].seq
	})
	ids := make([]string, len(ex))
	for i, c := range ex {
		ids[i] = c.id
	}
	return ids
}

func validW(P, O, W int64) bool {
	return P >= 1 && P <= 1e9 && O >= 0 && O < P && W >= 1 && W <= P
}

func validT(t int64) bool { return t >= 0 && t <= 1e12 }

func (n *naive) register(dev string, P, O, W, now int64) string {
	if !validW(P, O, W) || !validT(now) {
		return "Invalid"
	}
	if d, ok := n.devs[dev]; ok {
		if now < d.now {
			return "ClockBack"
		}
		return "Exists"
	}
	n.devs[dev] = &nDev{cur: nParam{P, O, W}, eff: 0, now: now, curWin: -1}
	return "ok"
}

func (n *naive) reconfig(dev string, P, O, W, now int64) string {
	if !validW(P, O, W) || !validT(now) {
		return "Invalid"
	}
	d, ok := n.devs[dev]
	if !ok {
		return "NoDevice"
	}
	if now < d.now {
		return "ClockBack"
	}
	eff := now
	if s, in := d.windowAt(now); in {
		var length int64
		if d.hasPrev && s == d.prevS {
			length = d.prevE - d.prevS
		} else {
			length = d.cur.W
		}
		d.prevS, d.prevE, d.hasPrev = s, s+length, true
		eff = s + length
	} else {
		d.hasPrev = false
	}
	d.cur, d.eff, d.now = nParam{P, O, W}, eff, now
	return "ok"
}

func (n *naive) enqueue(dev, id string, size int64, prio int, expire, now int64) string {
	if id == "" || size < 1 || size > 1e6 || prio < 0 || prio > 3 ||
		!validT(expire) || !validT(now) {
		return "Invalid"
	}
	d, ok := n.devs[dev]
	if !ok {
		return "NoDevice"
	}
	if now < d.now {
		return "ClockBack"
	}
	for _, c := range d.cmds {
		if n.alive(c) && c.id == id {
			return "DupCmd"
		}
	}
	if size > n.Bw {
		return "TooBig"
	}
	if expire <= d.nextAvail(now) {
		return "Unreachable"
	}
	live := 0
	for _, c := range d.cmds {
		if n.alive(c) && c.expire > now {
			live++
		}
	}
	if live >= n.Q {
		return "Full"
	}
	// 接受：此时才清过期。
	n.purge(d, now)
	d.cmds = append(d.cmds, &nCmd{id: id, size: size, prio: prio, expire: expire,
		seq: d.seqGen, status: "pending", win: -1, first: -1})
	d.seqGen++
	d.now = now
	return "ok"
}

func (n *naive) ack(dev, id string, now int64) string {
	if !validT(now) {
		return "Invalid"
	}
	d, ok := n.devs[dev]
	if !ok {
		return "NoDevice"
	}
	if now < d.now {
		return "ClockBack"
	}
	for _, c := range d.cmds {
		if c.id == id && n.alive(c) && c.status == "inflight" && now < c.expire {
			c.outcome = "Acked"
			d.now = now
			return "ok"
		}
	}
	return "NoCmd"
}

type nResult struct {
	delivered []string
	expired   []string
	failed    []string
	err       string
	examined  int
}

// deliver 完全按题面四步线性模拟。
func (n *naive) deliver(dev string, now int64) nResult {
	var res nResult
	res.delivered, res.expired, res.failed = []string{}, []string{}, []string{}
	if !validT(now) {
		res.err = "Invalid"
		return res
	}
	d, ok := n.devs[dev]
	if !ok {
		res.err = "NoDevice"
		return res
	}
	if now < d.now {
		res.err = "ClockBack"
		return res
	}
	win, in := d.windowAt(now)
	if !in {
		res.err = "Asleep"
		return res
	}
	// ① 清过期。
	res.expired = n.purge(d, now)
	// ② 换窗：最后投递窗口早于当前窗口的待确认指令，达 R 次记 Failed。
	if d.curWin != win {
		var old []*nCmd
		for _, c := range d.cmds {
			if n.alive(c) && c.status == "inflight" && c.win == d.curWin {
				old = append(old, c)
			}
		}
		sort.Slice(old, func(i, j int) bool {
			if old[i].first != old[j].first {
				return old[i].first < old[j].first
			}
			return old[i].seq < old[j].seq
		})
		var kept []*nCmd
		for _, c := range old {
			if c.sent >= n.R {
				c.outcome = "Failed"
				res.failed = append(res.failed, c.id)
			} else {
				kept = append(kept, c)
			}
		}
		_ = kept
		d.curWin, d.winUsed, d.winBytes = win, 0, 0
	}
	// ③ 组候选：重投（win<当前窗）按 first,seq；未投递按 prio 降、seq 升。
	var cand []*nCmd
	var rein []*nCmd
	for _, c := range d.cmds {
		if n.alive(c) && c.status == "inflight" && c.win < win {
			rein = append(rein, c)
		}
	}
	sort.Slice(rein, func(i, j int) bool {
		if rein[i].first != rein[j].first {
			return rein[i].first < rein[j].first
		}
		return rein[i].seq < rein[j].seq
	})
	var pend []*nCmd
	for _, c := range d.cmds {
		if n.alive(c) && c.status == "pending" {
			pend = append(pend, c)
		}
	}
	sort.Slice(pend, func(i, j int) bool {
		if pend[i].prio != pend[j].prio {
			return pend[i].prio > pend[j].prio
		}
		return pend[i].seq < pend[j].seq
	})
	cand = append(cand, rein...)
	cand = append(cand, pend...)
	// ④ 逐条取：K 用尽即停；第一条放不下即停。
	remN := n.K - d.winUsed
	remB := n.Bw - d.winBytes
	for _, c := range cand {
		if remN <= 0 {
			break
		}
		res.examined++
		if c.size > remB {
			break
		}
		if c.sent == 0 {
			c.first = win
		}
		c.sent++
		c.win = win
		c.status = "inflight"
		remN--
		remB -= c.size
		d.winUsed++
		d.winBytes += c.size
		res.delivered = append(res.delivered, c.id)
	}
	d.now = now
	return res
}
