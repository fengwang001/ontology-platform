package sched_test

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/sched"
)

type nSt int

const (
	nP nSt = iota
	nI
	nA
	nE
	nF
)

type nCmd struct {
	id                   string
	size, prio, expire   int64
	seq                  int64
	st                   nSt
	sends, firstW, lastW int64
	sendOrd              int64
}

type nDev struct {
	p, o, w           int64
	has               bool
	np, no, nw, grace int64
	winS, winK, winBw int64
	cmds              map[string]*nCmd
	seq               int64
	sendTick          int64
}

type naive struct {
	k, bw, r, q, last int64
	devs              map[string]*nDev
}

type nRes struct {
	ids, expired, failed []string
	err                  string
	e                    int64
}

func nFirst(p, o, from int64) int64 {
	if from <= o {
		return o
	}
	return o + ((from-o+p-1)/p)*p
}

func (m *naive) eff(d *nDev, now int64) (int64, int64, int64) {
	if d.has && now >= d.grace {
		d.p, d.o, d.w, d.has = d.np, d.no, d.nw, false
	}
	return d.p, d.o, d.w
}

func nWin(p, o, w, now int64) (int64, bool) {
	if now < o {
		return 0, false
	}
	s := o + ((now-o)/p)*p
	return s, now < s+w
}

func (m *naive) purge(d *nDev, now int64) []string {
	var dead []*nCmd
	for _, c := range d.cmds {
		if (c.st == nP || c.st == nI) && c.expire <= now {
			dead = append(dead, c)
		}
	}
	sort.Slice(dead, func(i, j int) bool {
		if dead[i].expire != dead[j].expire {
			return dead[i].expire < dead[j].expire
		}
		return dead[i].seq < dead[j].seq
	})
	var ids []string
	for _, c := range dead {
		c.st = nE
		ids = append(ids, c.id)
	}
	return ids
}

func (m *naive) active(d *nDev) int64 {
	var n int64
	for _, c := range d.cmds {
		if c.st == nP || c.st == nI {
			n++
		}
	}
	return n
}

func nValid(p sched.Params) bool {
	return p.P >= 1 && p.P <= 1e9 && p.O >= 0 && p.O < p.P && p.W >= 1 && p.W <= p.P
}

func nBadTime(now int64) bool { return now < 0 || now > 1e12 }

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (m *naive) Register(id string, p sched.Params, now int64) (r nRes) {
	if id == "" || !nValid(p) || nBadTime(now) {
		r.err = sched.ErrInvalid.Error()
		return
	}
	if now < m.last {
		r.err = sched.ErrClockBack.Error()
		return
	}
	if _, ok := m.devs[id]; ok {
		r.err = sched.ErrExists.Error()
		return
	}
	m.last = now
	m.devs[id] = &nDev{p: p.P, o: p.O, w: p.W, cmds: map[string]*nCmd{}, winS: -1}
	return
}

func (m *naive) Reconfigure(id string, p sched.Params, now int64) (r nRes) {
	if id == "" || !nValid(p) || nBadTime(now) {
		r.err = sched.ErrInvalid.Error()
		return
	}
	if now < m.last {
		r.err = sched.ErrClockBack.Error()
		return
	}
	d, ok := m.devs[id]
	if !ok {
		r.err = sched.ErrNoDevice.Error()
		return
	}
	m.last = now
	pp, oo, ww := m.eff(d, now)
	e := now
	if now >= oo {
		if s, in := nWin(pp, oo, ww, now); in {
			e = s + ww
		}
	}
	d.has, d.np, d.no, d.nw, d.grace = true, p.P, p.O, p.W, e
	r.e = e
	return
}

func (m *naive) Enqueue(dev, id string, size, prio, expire, now int64) (r nRes) {
	if dev == "" || id == "" || size < 1 || size > 1e6 || prio < 0 || prio > 3 ||
		nBadTime(now) || nBadTime(expire) {
		r.err = sched.ErrInvalid.Error()
		return
	}
	if now < m.last {
		r.err = sched.ErrClockBack.Error()
		return
	}
	d, ok := m.devs[dev]
	if !ok {
		r.err = sched.ErrNoDevice.Error()
		return
	}
	if c, dup := d.cmds[id]; dup && (c.st == nP || c.st == nI) {
		r.err = sched.ErrDupCmd.Error()
		return
	}
	if size > m.bw {
		r.err = sched.ErrTooBig.Error()
		return
	}
	pp, oo, ww := m.eff(d, now)
	var s int64
	if _, in := nWin(pp, oo, ww, now); in {
		s = now
	} else {
		s = nFirst(pp, oo, now)
	}
	if expire <= s {
		r.err = sched.ErrUnreachable.Error()
		return
	}
	m.purge(d, now)
	if m.active(d) >= m.q {
		r.err = sched.ErrFull.Error()
		return
	}
	m.last = now
	d.seq++
	d.cmds[id] = &nCmd{id: id, size: size, prio: prio, expire: expire, seq: d.seq, st: nP}
	return
}

func (m *naive) Deliver(dev string, now int64) (r nRes) {
	if dev == "" || nBadTime(now) {
		r.err = sched.ErrInvalid.Error()
		return
	}
	if now < m.last {
		r.err = sched.ErrClockBack.Error()
		return
	}
	d, ok := m.devs[dev]
	if !ok {
		r.err = sched.ErrNoDevice.Error()
		return
	}
	pp, oo, ww := m.eff(d, now)
	start, in := nWin(pp, oo, ww, now)
	if !in {
		r.err = sched.ErrAsleep.Error()
		return
	}
	m.last = now
	r.expired = m.purge(d, now)
	if d.winS != start {
		d.winS, d.winK, d.winBw = start, 0, 0
	}

	var infl []*nCmd
	for _, c := range d.cmds {
		if c.st == nI {
			infl = append(infl, c)
		}
	}
	sort.Slice(infl, func(i, j int) bool {
		if infl[i].firstW != infl[j].firstW {
			return infl[i].firstW < infl[j].firstW
		}
		return infl[i].sendOrd < infl[j].sendOrd
	})

	var cand []*nCmd
	for _, c := range infl {
		if c.lastW < start {
			if c.sends >= m.r {
				c.st = nF
				r.failed = append(r.failed, c.id)
			} else {
				cand = append(cand, c)
			}
		}
	}
	var pend []*nCmd
	for _, c := range d.cmds {
		if c.st == nP {
			pend = append(pend, c)
		}
	}
	sort.Slice(pend, func(i, j int) bool {
		if pend[i].prio != pend[j].prio {
			return pend[i].prio > pend[j].prio
		}
		return pend[i].seq < pend[j].seq
	})
	cand = append(cand, pend...)

	usedK, usedBw := d.winK, d.winBw
	for _, c := range cand {
		if usedK >= m.k || usedBw+c.size > m.bw {
			break
		}
		c.sends++
		c.lastW = start
		if c.st == nP {
			c.st = nI
			c.firstW = start
			d.sendTick++
			c.sendOrd = d.sendTick
		}
		usedK++
		usedBw += c.size
		r.ids = append(r.ids, c.id)
	}
	d.winK, d.winBw = usedK, usedBw
	return
}

func (m *naive) Ack(dev, id string, now int64) (r nRes) {
	if dev == "" || id == "" || nBadTime(now) {
		r.err = sched.ErrInvalid.Error()
		return
	}
	if now < m.last {
		r.err = sched.ErrClockBack.Error()
		return
	}
	d, ok := m.devs[dev]
	if !ok {
		r.err = sched.ErrNoDevice.Error()
		return
	}
	c, ok := d.cmds[id]
	if !ok || c.st != nI || now >= c.expire {
		r.err = sched.ErrNoCmd.Error()
		return
	}
	m.last = now
	c.st = nA
	return
}

func etoken(msg string) string {
	switch {
	case msg == "":
		return ""
	case strings.Contains(msg, "invalid"):
		return "INVALID"
	case strings.Contains(msg, "backwards"):
		return "CLOCKBACK"
	case strings.Contains(msg, "asleep"), strings.Contains(msg, "receive window"):
		return "ASLEEP"
	case strings.Contains(msg, "registered"):
		return "EXISTS"
	case strings.Contains(msg, "not found"):
		return "NODEVICE"
	case strings.Contains(msg, "duplicate"):
		return "DUP"
	case strings.Contains(msg, "too big"):
		return "TOOBIG"
	case strings.Contains(msg, "before next"):
		return "UNREACHABLE"
	case strings.Contains(msg, "queue full"):
		return "FULL"
	case strings.Contains(msg, "no such"):
		return "NOCMD"
	default:
		return "?" + msg
	}
}

type op struct {
	kind                    int // 0 reg 1 reconf 2 enq 3 deliver 4 ack
	dev, id                 string
	size, prio, expire, now int64
	p                       sched.Params
}

func rp(rng *rand.Rand) sched.Params {
	p := int64(rng.Intn(24) + 1)
	o := int64(rng.Intn(int(p)))
	w := int64(rng.Intn(int(p)) + 1)
	return sched.Params{P: p, O: o, W: w}
}

func rlist(xs []string) string { return "[" + strings.Join(xs, ",") + "]" }

func (r nRes) short() string {
	parts := []string{"ids=" + rlist(r.ids), "exp=" + rlist(r.expired), "fail=" + rlist(r.failed)}
	if r.err != "" {
		parts = append(parts, "err="+etoken(r.err))
	}
	if r.e != 0 {
		parts = append(parts, fmt.Sprintf("e=%d", r.e))
	}
	return strings.Join(parts, " ")
}

func sameRes(a, b nRes) (string, bool) {
	if etoken(a.err) != etoken(b.err) {
		return "error mismatch", false
	}
	if a.err != "" {
		return "", true
	}
	if !eqStr(a.ids, b.ids) {
		return "delivered ids mismatch", false
	}
	if !eqStr(a.expired, b.expired) {
		return "expired mismatch", false
	}
	if !eqStr(a.failed, b.failed) {
		return "failed mismatch", false
	}
	if a.e != b.e {
		return "effective-time mismatch", false
	}
	return "", true
}

func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 1500
	for seed := int64(0); seed < sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		k := int64(rng.Intn(3) + 1)
		bw := int64(rng.Intn(120) + 1)
		r := int64(rng.Intn(3) + 1)
		qc := int64(rng.Intn(6) + 1)

		sc := sched.New(k, bw, r, qc)
		m := &naive{k: k, bw: bw, r: r, q: qc, last: -1, devs: map[string]*nDev{}}
		var log strings.Builder
		fmt.Fprintf(&log, "seed=%d K=%d Bw=%d R=%d Q=%d\n", seed, k, bw, r, qc)

		dev := "d0"
		p0 := rp(rng)
		t0 := int64(rng.Intn(20))

		run := func(o op, got, want nRes) {
			why, ok := sameRes(got, want)
			fmt.Fprintf(&log, "  %s -> %s\n", descOp(o), got.short())
			if !ok {
				fmt.Fprintf(&log, "  WANT %s\n  !! %s\n", want.short(), why)
				t.Fatalf("seed=%d %s\n%s", seed, why, log.String())
			}
		}

		run(op{kind: 0, dev: dev, p: p0, now: t0},
			nRes{err: errStr(sc.Register(dev, p0, t0))},
			m.Register(dev, p0, t0))

		now := t0
		ids := []string{}
		addID := func(s string) {
			for _, x := range ids {
				if x == s {
					return
				}
			}
			ids = append(ids, s)
		}

		steps := 60 + rng.Intn(60)
		for i := 0; i < steps; i++ {
			kind := rng.Intn(10)
			switch {
			case kind < 5: // enqueue 50%
				now += int64(rng.Intn(40))
				name := fmt.Sprintf("c%03d", rng.Intn(14))
				addID(name)
				size := int64(rng.Intn(int(bw)+40) + 1)
				prio := int64(rng.Intn(4))
				expire := now + int64(rng.Intn(int(4*p0.P)+1))
				if rng.Intn(6) == 0 {
					expire = now // 恰等过期路径
				}
				o := op{kind: 2, dev: dev, id: name, size: size, prio: prio, expire: expire, now: now}
				run(o, nRes{err: errStr(sc.Enqueue(dev, name, size, prio, expire, now))},
					m.Enqueue(dev, name, size, prio, expire, now))
			case kind < 8: // deliver 30%，多落在窗口附近
				now += int64(rng.Intn(int(p0.P) + 1))
				o := op{kind: 3, dev: dev, now: now}
				d, err := sc.Deliver(dev, now)
				got := nRes{err: errStr(err)}
				if err == nil {
					got.ids, got.expired, got.failed = d.IDs, d.Expired, d.Failed
				}
				want := m.Deliver(dev, now)
				run(o, got, want)
				if err == nil {
					ex, _ := sc.Examined(dev)
					bound := len(got.ids) + len(got.expired) + len(got.failed) + 1
					if ex > bound {
						t.Fatalf("seed=%d examined=%d > bound %d at now=%d", seed, ex, bound, now)
					}
				}
			case kind == 8: // ack 10%
				now += int64(rng.Intn(int(p0.P) + 1))
				var id string
				if len(ids) > 0 {
					id = ids[rng.Intn(len(ids))]
				} else {
					id = "none"
				}
				o := op{kind: 4, dev: dev, id: id, now: now}
				run(o, nRes{err: errStr(sc.Ack(dev, id, now))}, m.Ack(dev, id, now))
			default: // reconfigure 10%
				now += int64(rng.Intn(int(p0.P) + 1))
				np := rp(rng)
				p0 = np
				o := op{kind: 1, dev: dev, p: np, now: now}
				e, err := sc.Reconfigure(dev, np, now)
				run(o, nRes{e: e, err: errStr(err)}, m.Reconfigure(dev, np, now))
			}
		}
		if testing.Verbose() {
			t.Logf("\n%s", log.String())
		}
	}
}

func descOp(o op) string {
	switch o.kind {
	case 0:
		return fmt.Sprintf("Register(%s P=%d o=%d w=%d now=%d)", o.dev, o.p.P, o.p.O, o.p.W, o.now)
	case 1:
		return fmt.Sprintf("Reconfigure(%s P=%d o=%d w=%d now=%d)", o.dev, o.p.P, o.p.O, o.p.W, o.now)
	case 2:
		return fmt.Sprintf("Enqueue(%s %s size=%d prio=%d exp=%d now=%d)",
			o.dev, o.id, o.size, o.prio, o.expire, o.now)
	case 3:
		return fmt.Sprintf("Deliver(%s now=%d)", o.dev, o.now)
	default:
		return fmt.Sprintf("Ack(%s %s now=%d)", o.dev, o.id, o.now)
	}
}
