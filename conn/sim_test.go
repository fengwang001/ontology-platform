package conn_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"ontology/cert"
	"ontology/conn"
	"ontology/rotate"
)

// 朴素模拟：无堆、无事务，每操作整体克隆，逐证书扫描结算，
// 语义与三包实现完全一致，用于对照。
type naiveCert struct {
	serial   string
	dev      string
	nb, na   int64
	state    cert.State
	lapseAt  int64
	retireAt int64
}

type naive struct {
	g, t, w  int64
	now      int64
	certs    map[string]*naiveCert
	sessions map[string]string
}

func newNaive(g, confirm, window int64) *naive {
	return &naive{
		g: g, t: confirm, w: window,
		certs:    make(map[string]*naiveCert),
		sessions: make(map[string]string),
	}
}

func (n *naive) clone() *naive {
	m := &naive{g: n.g, t: n.t, w: n.w, now: n.now,
		certs:    make(map[string]*naiveCert, len(n.certs)),
		sessions: make(map[string]string, len(n.sessions))}
	for k, c := range n.certs {
		cp := *c
		m.certs[k] = &cp
	}
	for k, v := range n.sessions {
		m.sessions[k] = v
	}
	return m
}

// settle 扫描全部证书，按 (发生时刻, 设备字节序, 序列号) 结算到期迁移。
func (n *naive) settle(now int64) []string {
	type mig struct {
		at     int64
		dev    string
		serial string
		to     cert.State
	}
	var migs []mig
	for _, c := range n.certs {
		switch c.state {
		case cert.Pending:
			if now >= c.lapseAt {
				migs = append(migs, mig{c.lapseAt, c.dev, c.serial, cert.Lapsed})
			}
		case cert.Retiring:
			if now >= c.retireAt {
				migs = append(migs, mig{c.retireAt, c.dev, c.serial, cert.Retired})
			}
		}
	}
	sort.Slice(migs, func(i, j int) bool {
		a, b := migs[i], migs[j]
		if a.at != b.at {
			return a.at < b.at
		}
		if a.dev != b.dev {
			return a.dev < b.dev
		}
		return a.serial < b.serial
	})
	var kicked []string
	for _, m := range migs {
		c := n.certs[m.serial]
		c.state = m.to
		if m.to == cert.Retired && n.sessions[c.dev] == c.serial {
			delete(n.sessions, c.dev)
			kicked = append(kicked, c.dev)
		}
	}
	return kicked
}

func (n *naive) slot(dev string, st cert.State) *naiveCert {
	for _, c := range n.certs {
		if c.dev == dev && c.state == st {
			return c
		}
	}
	return nil
}

func validTime(now int64) bool { return now >= 0 && now <= cert.MaxTime }

func (n *naive) issue(dev, serial string, nb, na, now int64) ([]string, error) {
	if dev == "" || serial == "" || nb < 0 || nb >= na || na > cert.MaxTime || !validTime(now) {
		return nil, cert.ErrInvalid
	}
	if now < n.now {
		return nil, cert.ErrClockBack
	}
	w := n.clone()
	kicked := w.settle(now)
	if _, ok := w.certs[serial]; ok {
		return nil, cert.ErrDupSerial
	}
	if w.slot(dev, cert.Pending) != nil {
		return nil, cert.ErrPendingExists
	}
	c := &naiveCert{serial: serial, dev: dev, nb: nb, na: na}
	if a := w.slot(dev, cert.Active); a != nil {
		if now < a.na-n.w {
			return nil, cert.ErrTooEarly
		}
		c.state = cert.Pending
		c.lapseAt = max(now, nb) + n.t
	} else {
		c.state = cert.Active
	}
	w.certs[serial] = c
	w.now = now
	*n = *w
	return kicked, nil
}

func (n *naive) revoke(serial string, now int64) ([]string, error) {
	if serial == "" || !validTime(now) {
		return nil, cert.ErrInvalid
	}
	if now < n.now {
		return nil, cert.ErrClockBack
	}
	w := n.clone()
	kicked := w.settle(now)
	c, ok := w.certs[serial]
	if !ok {
		return nil, cert.ErrUnknown
	}
	if c.state.Final() {
		return nil, cert.ErrFinal
	}
	c.state = cert.Revoked
	if w.sessions[c.dev] == c.serial {
		delete(w.sessions, c.dev)
		kicked = append(kicked, c.dev)
	}
	w.now = now
	*n = *w
	return kicked, nil
}

func (n *naive) connect(dev, serial string, now int64) ([]string, error) {
	if dev == "" || serial == "" || !validTime(now) {
		return nil, cert.ErrInvalid
	}
	if now < n.now {
		return nil, cert.ErrClockBack
	}
	w := n.clone()
	kicked := w.settle(now)
	c, ok := w.certs[serial]
	if !ok {
		return nil, cert.ErrUnknown
	}
	if c.dev != dev {
		return nil, cert.ErrMismatch
	}
	switch c.state {
	case cert.Revoked:
		return nil, cert.ErrRevoked
	case cert.Retired:
		return nil, cert.ErrRetired
	case cert.Lapsed:
		return nil, cert.ErrLapsed
	}
	if now < c.nb {
		return nil, cert.ErrNotYet
	}
	if now >= c.na {
		return nil, cert.ErrExpired
	}
	if c.state == cert.Pending {
		oldActive := w.slot(dev, cert.Active)
		oldRetiring := w.slot(dev, cert.Retiring)
		c.state = cert.Active
		if oldActive != nil {
			oldActive.state = cert.Retiring
			oldActive.retireAt = min(now+n.g, oldActive.na)
		}
		if oldRetiring != nil {
			oldRetiring.state = cert.Retired
			if w.sessions[dev] == oldRetiring.serial {
				delete(w.sessions, dev)
				kicked = append(kicked, dev)
			}
		}
	}
	w.sessions[dev] = serial
	w.now = now
	*n = *w
	return kicked, nil
}

func (n *naive) disconnect(dev string, now int64) ([]string, error) {
	if dev == "" || !validTime(now) {
		return nil, cert.ErrInvalid
	}
	if now < n.now {
		return nil, cert.ErrClockBack
	}
	w := n.clone()
	kicked := w.settle(now)
	if _, ok := w.sessions[dev]; !ok {
		return nil, cert.ErrNoSession
	}
	delete(w.sessions, dev)
	w.now = now
	*n = *w
	return kicked, nil
}

// realSystem 把三包组合成与 naive 相同的接口。
type realSystem struct {
	st *cert.Store
	r  *rotate.Service
	c  *conn.Service
}

func newReal(g, confirm, window int64) *realSystem {
	st, err := cert.NewStore(g, confirm, window)
	if err != nil {
		panic(err)
	}
	return &realSystem{st: st, r: rotate.New(st), c: conn.New(st)}
}

func (s *realSystem) issue(dev, serial string, nb, na, now int64) ([]string, error) {
	return s.r.Issue(dev, serial, nb, na, now)
}
func (s *realSystem) revoke(serial string, now int64) ([]string, error) {
	return s.r.Revoke(serial, now)
}
func (s *realSystem) connect(dev, serial string, now int64) ([]string, error) {
	return s.c.Connect(dev, serial, now)
}
func (s *realSystem) disconnect(dev string, now int64) ([]string, error) {
	return s.c.Disconnect(dev, now)
}

type system interface {
	issue(dev, serial string, nb, na, now int64) ([]string, error)
	revoke(serial string, now int64) ([]string, error)
	connect(dev, serial string, now int64) ([]string, error)
	disconnect(dev string, now int64) ([]string, error)
}

type opKind int

const (
	opIssue opKind = iota
	opConnect
	opDisconnect
	opRevoke
)

type opArgs struct {
	kind        opKind
	dev, serial string
	nb, na, now int64
}

func (o opArgs) String() string {
	switch o.kind {
	case opIssue:
		return fmt.Sprintf("Issue(%s,%s,nb=%d,na=%d,now=%d)", o.dev, o.serial, o.nb, o.na, o.now)
	case opConnect:
		return fmt.Sprintf("Connect(%s,%s,now=%d)", o.dev, o.serial, o.now)
	case opDisconnect:
		return fmt.Sprintf("Disconnect(%s,now=%d)", o.dev, o.now)
	default:
		return fmt.Sprintf("Revoke(%s,now=%d)", o.serial, o.now)
	}
}

func runOp(s system, o opArgs) ([]string, error) {
	switch o.kind {
	case opIssue:
		return s.issue(o.dev, o.serial, o.nb, o.na, o.now)
	case opConnect:
		return s.connect(o.dev, o.serial, o.now)
	case opDisconnect:
		return s.disconnect(o.dev, o.now)
	default:
		return s.revoke(o.serial, o.now)
	}
}

var fuzzDevs = []string{"a", "b", "c", "dev-1", "dev-2", "edge"}

var fuzzSerials = []string{"s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8"}

func genOps(r *rand.Rand, n int) []opArgs {
	ops := make([]opArgs, 0, n)
	now := int64(0)
	fresh := 0
	type known struct{ dev, serial string }
	var issued []known // 可能含签发失败的，无妨
	pickKnown := func() (string, string) {
		k := issued[r.Intn(len(issued))]
		return k.dev, k.serial
	}
	for i := 0; i < n; i++ {
		switch x := r.Intn(100); {
		case x < 70: // 时钟前进
			now += r.Int63n(80)
		case x < 85: // 时钟不变
		default: // 时钟回退（不越过 0）
			if back := r.Int63n(30); back <= now {
				now -= back
			}
		}
		dev := fuzzDevs[r.Intn(len(fuzzDevs))]
		switch r.Intn(100) {
		case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19,
			20, 21, 22, 23, 24, 25, 26, 27, 28, 29:
			serial := fuzzSerials[r.Intn(len(fuzzSerials))]
			if r.Intn(100) < 60 {
				fresh++
				serial = fmt.Sprintf("n%d", fresh)
			}
			nb := now - 50 + r.Int63n(200)
			if nb < 0 {
				nb = 0
			}
			na := nb + 1 + r.Int63n(1500)
			if r.Intn(100) < 5 { // 少量非法参数
				na = nb
			}
			ops = append(ops, opArgs{kind: opIssue, dev: dev, serial: serial, nb: nb, na: na, now: now})
			issued = append(issued, known{dev, serial})
		case 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49,
			50, 51, 52, 53, 54, 55, 56, 57, 58, 59:
			serial := ""
			if len(issued) > 0 && r.Intn(100) < 65 {
				dev, serial = pickKnown() // 设备与证书匹配，深入准入流程
			} else {
				serial = fuzzSerials[r.Intn(len(fuzzSerials))]
				if r.Intn(100) < 20 {
					serial = fmt.Sprintf("n%d", r.Intn(fresh+2))
				}
			}
			ops = append(ops, opArgs{kind: opConnect, dev: dev, serial: serial, now: now})
		case 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74:
			ops = append(ops, opArgs{kind: opDisconnect, dev: dev, now: now})
		default:
			var serial string
			if len(issued) > 0 && r.Intn(100) < 60 {
				_, serial = pickKnown()
			} else {
				serial = fuzzSerials[r.Intn(len(fuzzSerials))]
				if r.Intn(100) < 30 {
					serial = fmt.Sprintf("n%d", r.Intn(fresh+2))
				}
			}
			ops = append(ops, opArgs{kind: opRevoke, serial: serial, now: now})
		}
	}
	return ops
}

func equalKicked(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// 1500 组随机操作序列：三包实现与逐步朴素模拟逐步对照，
// 日志打印输入、输出与判定依据。
func TestFuzzAgainstNaiveSimulation(t *testing.T) {
	const sequences = 1500
	const opsPerSeq = 30
	const g, confirm, window = int64(40), int64(100), int64(100)
	verdicts := make(map[string]int)
	for seed := int64(0); seed < sequences; seed++ {
		ops := genOps(rand.New(rand.NewSource(seed)), opsPerSeq)
		real := newReal(g, confirm, window)
		sim := newNaive(g, confirm, window)
		t.Logf("序列 %d：%d 个操作（G=%d T=%d W=%d）", seed, len(ops), g, confirm, window)
		for i, o := range ops {
			gotK, gotErr := runOp(real, o)
			wantK, wantErr := runOp(sim, o)
			verdict := "接受"
			if wantErr != nil {
				verdict = "拒绝: " + errString(wantErr)
			} else if len(wantK) > 0 {
				verdict = "接受(含踢线)"
			}
			verdicts[verdict]++
			t.Logf("  op[%d] %s -> kicked=%v 判定=%s", i, o, wantK, verdict)
			if !equalKicked(gotK, wantK) || gotErr != wantErr {
				t.Fatalf("序列 %d op[%d] %s:\n实现 kicked=%v err=%v\n模拟 kicked=%v err=%v",
					seed, i, o, gotK, errString(gotErr), wantK, errString(wantErr))
			}
			if got, want := real.st.Now(), sim.now; got != want {
				t.Fatalf("序列 %d op[%d] %s: now=%d, 模拟 %d", seed, i, o, got, want)
			}
		}
		// 终态全量对照。
		certs, sessions, now := real.st.Dump()
		if now != sim.now {
			t.Fatalf("序列 %d: now=%d, 模拟 %d", seed, now, sim.now)
		}
		if !reflect.DeepEqual(sessions, sim.sessions) {
			t.Fatalf("序列 %d: sessions=%v, 模拟 %v", seed, sessions, sim.sessions)
		}
		gotStates := make(map[string]cert.State, len(certs))
		for _, c := range certs {
			gotStates[c.Serial] = c.State
		}
		wantStates := make(map[string]cert.State, len(sim.certs))
		for s, c := range sim.certs {
			wantStates[s] = c.state
		}
		if !reflect.DeepEqual(gotStates, wantStates) {
			t.Fatalf("序列 %d: states=%v, 模拟 %v", seed, gotStates, wantStates)
		}
	}
	t.Logf("判定分布: %v", verdicts)
}
