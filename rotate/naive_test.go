package rotate

// 本文件实现与规范逐条对应的逐步朴素模拟，并对 1500 组随机操作序列做差分测试：
// 真实内核（rotate + conn 钩子）的每次输出、终态、会话、踢线都必须与朴素模型一致。

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"ontology/cert"
)

type naiveCert struct {
	dev               string
	nb, na            int64
	st                cert.State
	lapseAt, retireAt int64
}

type naiveModel struct {
	g, ttl, w int64
	lastNow   int64
	certs     map[string]*naiveCert
	sessions  map[string]string
}

func newNaive(g, ttl, w int64) *naiveModel {
	return &naiveModel{g: g, ttl: ttl, w: w,
		certs: map[string]*naiveCert{}, sessions: map[string]string{}}
}

func (n *naiveModel) clone() *naiveModel {
	c := &naiveModel{g: n.g, ttl: n.ttl, w: n.w, lastNow: n.lastNow,
		certs:    make(map[string]*naiveCert, len(n.certs)),
		sessions: make(map[string]string, len(n.sessions))}
	for k, v := range n.certs {
		vv := *v
		c.certs[k] = &vv
	}
	for k, v := range n.sessions {
		c.sessions[k] = v
	}
	return c
}

func (n *naiveModel) absorb(c *naiveModel) {
	n.certs, n.sessions, n.lastNow = c.certs, c.sessions, c.lastNow
}

type fired struct {
	at     int64
	dev    string
	kind   int
	serial string
}

// settle 反复全量扫描，按 (at, dev, kind: lapse<retire) 结算并返回踢线。
func (n *naiveModel) settle(now int64) []string {
	kicked := []string{}
	for {
		var evs []fired
		for sn, r := range n.certs {
			switch r.st {
			case cert.Pending:
				if now >= r.lapseAt {
					evs = append(evs, fired{r.lapseAt, r.dev, 0, sn})
				}
			case cert.Retiring:
				if now >= r.retireAt {
					evs = append(evs, fired{r.retireAt, r.dev, 1, sn})
				}
			}
		}
		if len(evs) == 0 {
			return kicked
		}
		sort.Slice(evs, func(i, j int) bool {
			if evs[i].at != evs[j].at {
				return evs[i].at < evs[j].at
			}
			if evs[i].dev != evs[j].dev {
				return evs[i].dev < evs[j].dev
			}
			return evs[i].kind < evs[j].kind
		})
		e := evs[0]
		r := n.certs[e.serial]
		if e.kind == 0 {
			r.st = cert.Lapsed
		} else {
			r.st = cert.Retired
			if n.sessions[e.dev] == e.serial {
				delete(n.sessions, e.dev)
				kicked = append(kicked, e.dev)
			}
		}
	}
}

func (n *naiveModel) find(dev string, st cert.State) string {
	for sn, r := range n.certs {
		if r.dev == dev && r.st == st {
			return sn
		}
	}
	return ""
}

type naiveOut struct {
	kicked []string
	err    error
	reason string
}

func (n *naiveModel) issue(dev, serial string, nb, na, now int64) naiveOut {
	c := cert.Cert{Serial: serial, Dev: dev, Nb: nb, Na: na}
	if !c.Valid() || !cert.ValidTime(now) {
		return naiveOut{reason: "invalid args -> ErrInvalid", err: cert.ErrInvalid}
	}
	snap := n.clone()
	if now < n.lastNow {
		return naiveOut{reason: "clock back -> ErrClockBack (no settle)", err: cert.ErrClockBack}
	}
	k := n.settle(now)
	n.lastNow = now
	if _, ok := n.certs[serial]; ok {
		n.absorb(snap)
		return naiveOut{reason: "serial exists -> ErrDupSerial", err: cert.ErrDupSerial}
	}
	if n.find(dev, cert.Pending) != "" {
		n.absorb(snap)
		return naiveOut{reason: "pending exists -> ErrPendingExists", err: cert.ErrPendingExists}
	}
	r := &naiveCert{dev: dev, nb: nb, na: na}
	if act := n.find(dev, cert.Active); act == "" {
		r.st = cert.Active
		n.certs[serial] = r
		return naiveOut{kicked: k, reason: "no active -> direct Active"}
	} else {
		old := n.certs[act]
		if now < old.na-n.w {
			n.absorb(snap)
			return naiveOut{reason: fmt.Sprintf("now<%d na-%d -> ErrTooEarly", old.na, n.w), err: cert.ErrTooEarly}
		}
		r.st = cert.Pending
		r.lapseAt = maxInt(now, nb) + n.ttl
		n.certs[serial] = r
		return naiveOut{kicked: k, reason: fmt.Sprintf("Pending lapseAt=%d", r.lapseAt)}
	}
}

func (n *naiveModel) revoke(serial string, now int64) naiveOut {
	if serial == "" || !cert.ValidTime(now) {
		return naiveOut{reason: "invalid -> ErrInvalid", err: cert.ErrInvalid}
	}
	snap := n.clone()
	if now < n.lastNow {
		return naiveOut{reason: "clock back", err: cert.ErrClockBack}
	}
	k := n.settle(now)
	n.lastNow = now
	r, ok := n.certs[serial]
	if !ok {
		n.absorb(snap)
		return naiveOut{reason: "unknown -> ErrUnknown", err: cert.ErrUnknown}
	}
	if r.st.Terminal() {
		n.absorb(snap)
		return naiveOut{reason: "terminal -> ErrFinal", err: cert.ErrFinal}
	}
	r.st = cert.Revoked
	if n.sessions[r.dev] == serial {
		delete(n.sessions, r.dev)
		k = append(k, r.dev)
	}
	return naiveOut{kicked: k, reason: fmt.Sprintf("revoked from state, kick via session check")}
}

func maxInt(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (n *naiveModel) connect(dev, serial string, now int64) naiveOut {
	if dev == "" || serial == "" || !cert.ValidTime(now) {
		return naiveOut{reason: "invalid", err: cert.ErrInvalid}
	}
	snap := n.clone()
	if now < n.lastNow {
		return naiveOut{reason: "clock back", err: cert.ErrClockBack}
	}
	k := n.settle(now)
	n.lastNow = now
	r, ok := n.certs[serial]
	if !ok {
		n.absorb(snap)
		return naiveOut{reason: "ErrUnknown", err: cert.ErrUnknown}
	}
	var e error
	switch {
	case r.dev != dev:
		e = cert.ErrMismatch
	case r.st == cert.Revoked:
		e = cert.ErrRevoked
	case r.st == cert.Retired:
		e = cert.ErrRetired
	case r.st == cert.Lapsed:
		e = cert.ErrLapsed
	case now < r.nb:
		e = cert.ErrNotYet
	case now >= r.na:
		e = cert.ErrExpired
	}
	if e != nil {
		n.absorb(snap)
		return naiveOut{reason: "admit: " + e.Error(), err: e}
	}
	// Pending 首次通过准入 -> 对调。
	if r.st == cert.Pending {
		oldActive := n.find(dev, cert.Active)
		r.st = cert.Active
		if oldActive != "" {
			if oldRet := n.find(dev, cert.Retiring); oldRet != "" {
				pr := n.certs[oldRet]
				pr.st = cert.Retired
				if n.sessions[dev] == oldRet {
					delete(n.sessions, dev)
					k = append(k, dev)
				}
			}
			oa := n.certs[oldActive]
			oa.st = cert.Retiring
			oa.retireAt = minInt(now+n.g, oa.na)
		}
	}
	n.sessions[dev] = serial // 接管不踢旧会话
	return naiveOut{kicked: k, reason: "admitted; session established/taken over"}
}

func minInt(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func (n *naiveModel) disconnect(dev string, now int64) naiveOut {
	if dev == "" || !cert.ValidTime(now) {
		return naiveOut{reason: "invalid", err: cert.ErrInvalid}
	}
	snap := n.clone()
	if now < n.lastNow {
		return naiveOut{reason: "clock back", err: cert.ErrClockBack}
	}
	k := n.settle(now)
	n.lastNow = now
	if _, ok := n.sessions[dev]; !ok {
		n.absorb(snap)
		return naiveOut{reason: "no session -> ErrNoSession", err: cert.ErrNoSession}
	}
	delete(n.sessions, dev)
	return naiveOut{kicked: k, reason: "session ended"}
}

type opKind int

const (
	opIssue opKind = iota
	opRevoke
	opConnect
	opDisconnect
)

type genOp struct {
	kind        opKind
	dev, serial string
	nb, na, now int64
	descr       string
}

// TestRandomDifferential 1500 组随机操作序列，真实实现与逐步朴素模拟逐操作比对。
func TestRandomDifferential(t *testing.T) {
	const sequences = 1500
	const opsPerSeq = 60
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 1))
		name := fmt.Sprintf("seq%d", seq)
		t.Run(name, func(t *testing.T) {
			runOneSequence(t, rng, opsPerSeq, seq)
		})
	}
}

func runOneSequence(t *testing.T, rng *rand.Rand, nOps, seq int) {
	t.Helper()
	g := int64(1 + rng.Intn(80))
	ttl := int64(1 + rng.Intn(120))
	w := int64(1 + rng.Intn(150))

	s, err := New(g, ttl, w)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m := newSessMgr(s)
	nm := newNaive(g, ttl, w)

	devs := []string{"d0", "d1", "d2", "d3"}
	var log []string
	now := int64(0)
	serialCounter := 0
	knownSerials := []string{}

	compare := func(step int, desc string, gotK []string, gotErr error, want naiveOut) {
		t.Helper()
		ek, wk := kicks(gotK), kicks(want.kicked)
		if !reflect.DeepEqual(ek, wk) || !sameErr(gotErr, want.err) {
			for _, l := range log {
				t.Log(l)
			}
			t.Fatalf("seq=%d step=%d %s\n got=(k=%v err=%v)\nwant=(k=%v err=%v) reason=%s",
				seq, step, desc, ek, gotErr, wk, want.err, want.reason)
		}
		if diff := diffStates(s, nm); diff != "" {
			for _, l := range log {
				t.Log(l)
			}
			t.Fatalf("seq=%d step=%d state mismatch after %s:\n%s", seq, step, desc, diff)
		}
		if diff := diffSessions(m, nm); diff != "" {
			t.Logf("real sessions=%v naive sessions=%v", m.sessions, nm.sessions)
			for _, l := range log {
				t.Log(l)
			}
			t.Fatalf("seq=%d step=%d sessions: %s", seq, step, diff)
		}
		log = append(log, fmt.Sprintf("step=%d %s => k=%v err=%v | %s",
			step, desc, ek, errName(gotErr), want.reason))
	}

	for step := 0; step < nOps; step++ {
		// 多数推进时间，小概率回退或停留。
		switch rng.Intn(10) {
		case 0:
			now -= int64(rng.Intn(3))
			if now < 0 {
				now = 0
			}
		case 1, 2:
			// stay
		default:
			now += int64(rng.Intn(60))
		}
		dev := devs[rng.Intn(len(devs))]
		k := opKind(rng.Intn(4))

		switch k {
		case opIssue:
			serialCounter++
			serial := fmt.Sprintf("s%03d", serialCounter)
			nb := int64(rng.Intn(700))
			na := nb + 1 + int64(rng.Intn(400))
			if rng.Intn(12) == 0 {
				na = nb // 故意非法
			}
			if rng.Intn(12) == 0 {
				serial = "" // 故意非法
			}
			gk, ge := s.Issue(dev, serial, nb, na, now)
			wo := nm.issue(dev, serial, nb, na, now)
			compare(step, fmt.Sprintf("Issue(%s,%s,nb=%d,na=%d,t=%d)", dev, serial, nb, na, now), gk, ge, wo)
			if ge == nil && serial != "" && nb < na {
				knownSerials = append(knownSerials, serial)
			}
		case opRevoke:
			serial := pickSerial(rng, knownSerials)
			if rng.Intn(8) == 0 {
				serial = fmt.Sprintf("ghost%d", rng.Intn(5))
			}
			gk, ge := s.Revoke(serial, now)
			wo := nm.revoke(serial, now)
			compare(step, fmt.Sprintf("Revoke(%s,t=%d)", serial, now), gk, ge, wo)
		case opConnect:
			serial := pickSerial(rng, knownSerials)
			if rng.Intn(8) == 0 {
				serial = fmt.Sprintf("ghost%d", rng.Intn(5))
			}
			gk, ge := m.Connect(dev, serial, now)
			wo := nm.connect(dev, serial, now)
			compare(step, fmt.Sprintf("Connect(%s,%s,t=%d)", dev, serial, now), gk, ge, wo)
		case opDisconnect:
			gk, ge := m.Disconnect(dev, now)
			wo := nm.disconnect(dev, now)
			compare(step, fmt.Sprintf("Disconnect(%s,t=%d)", dev, now), gk, ge, wo)
		}
	}
}

func pickSerial(rng *rand.Rand, pool []string) string {
	if len(pool) == 0 {
		return "s000"
	}
	return pool[rng.Intn(len(pool))]
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b)
}

func errName(e error) string {
	if e == nil {
		return "nil"
	}
	return e.Error()
}

type stateRow struct {
	serial, dev       string
	nb, na            int64
	st                cert.State
	lapseAt, retireAt int64
}

func realRows(s *Service) []stateRow {
	var rows []stateRow
	for sn, r := range s.bySerial {
		rows = append(rows, stateRow{sn, r.cert.Dev, r.cert.Nb, r.cert.Na,
			r.st, r.lapseAt, r.retireAt})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].serial < rows[j].serial })
	return rows
}

func naiveRows(n *naiveModel) []stateRow {
	var rows []stateRow
	for sn, r := range n.certs {
		rows = append(rows, stateRow{sn, r.dev, r.nb, r.na, r.st, r.lapseAt, r.retireAt})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].serial < rows[j].serial })
	return rows
}

func diffStates(s *Service, n *naiveModel) string {
	a, b := realRows(s), naiveRows(n)
	if len(a) != len(b) {
		return fmt.Sprintf("cert count %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			return fmt.Sprintf("row %d:\n real=%+v\nnaive=%+v", i, a[i], b[i])
		}
	}
	if s.lastNow != n.lastNow {
		return fmt.Sprintf("lastNow %d vs %d", s.lastNow, n.lastNow)
	}
	return ""
}

func diffSessions(m *sessMgr, n *naiveModel) string {
	devs := map[string]bool{}
	for d := range n.sessions {
		devs[d] = true
	}
	for _, snap := range m.s.Snapshot() {
		devs[snap.Cert.Dev] = true
	}
	for d := range devs {
		g, gok := m.SessionSerial(d)
		w, wok := n.sessions[d]
		if gok != wok || (gok && g != w) {
			return fmt.Sprintf("dev=%s session real=(%q,%v) naive=(%q,%v)", d, g, gok, w, wok)
		}
	}
	return ""
}
