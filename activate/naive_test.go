package activate_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/activate"
	"ontology/roster"
)

// 逐步朴素参考模型：独立按题目规则重新实现一份，逐操作与真实服务对照。

type refState int

const (
	rRegistered refState = iota
	rActivated
	rResetPending
)

type refSn struct {
	known     bool
	state     refState
	id, gen   int64
	fp        []byte
	until     int64
	tenant    string
	e, k      int
	lockUntil int64
}

type naive struct {
	m, lk  int64
	maxNow int64
	nextID int64
	quota  map[string]int
	used   map[string]int
	sns    map[string]*refSn
}

func newNaive(m, lk int64) *naive {
	return &naive{m: m, lk: lk, quota: map[string]int{}, used: map[string]int{}, sns: map[string]*refSn{}}
}

func validRefBytes(b []byte) bool { return len(b) >= 1 && len(b) <= 64 }
func validRefTime(t int64) bool   { return t >= 0 && t <= 1e12 }

func refFpEq(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (n *naive) addTenant(name string, q int) string {
	if name == "" || q < 1 || q > 1e6 {
		return "Invalid"
	}
	if _, ok := n.quota[name]; ok {
		return "Invalid"
	}
	n.quota[name] = q
	n.used[name] = 0
	return "OK"
}

func (n *naive) registerBatch(tenant string, until int64, sns [][]byte, now int64) string {
	if !validRefTime(until) || !validRefTime(now) || len(sns) < 1 || len(sns) > 1e4 {
		return "Invalid"
	}
	for _, s := range sns {
		if !validRefBytes(s) {
			return "Invalid"
		}
	}
	if _, ok := n.quota[tenant]; !ok {
		return "UnknownTenant"
	}
	if now < n.maxNow {
		return "ClockBack"
	}
	dup := -1
	seen := map[string]bool{}
	for i, s := range sns {
		key := string(s)
		if seen[key] || n.sns[key] != nil {
			if dup == -1 || i < dup {
				dup = i
			}
		}
		seen[key] = true
	}
	if dup != -1 {
		return fmt.Sprintf("DupSn(%d)", dup)
	}
	for _, s := range sns {
		n.sns[string(s)] = &refSn{known: true, state: rRegistered, until: until, tenant: tenant}
	}
	n.maxNow = now
	return "OK"
}

func (n *naive) activate(sn, fp []byte, now int64) string {
	if !validRefBytes(sn) || !validRefBytes(fp) || !validRefTime(now) {
		return "Invalid"
	}
	if now < n.maxNow {
		return "ClockBack"
	}
	s := n.sns[string(sn)]
	if s == nil {
		return "Unknown"
	}
	if now < s.lockUntil {
		return fmt.Sprintf("Locked(%d)", s.lockUntil)
	}
	switch s.state {
	case rRegistered:
		if now >= s.until {
			return "BatchClosed"
		}
		if n.used[s.tenant] >= n.quota[s.tenant] {
			return "Quota"
		}
		n.maxNow = now
		if s.id == 0 {
			n.nextID++
			s.id = n.nextID
		}
		s.gen++
		s.fp = append([]byte(nil), fp...)
		s.state = rActivated
		n.used[s.tenant]++
		return fmt.Sprintf("OK(%d,%d,false,%d)", s.id, s.gen, s.lockUntil)
	case rActivated:
		if !refFpEq(s.fp, fp) {
			n.maxNow = now
			s.e++
			if s.e >= int(n.m) {
				s.k++
				s.e = 0
				shift := s.k - 1
				if shift > 6 {
					shift = 6
				}
				s.lockUntil = now + n.lk<<uint(shift)
			}
			return fmt.Sprintf("Conflict(%d)", s.lockUntil)
		}
		n.maxNow = now
		return fmt.Sprintf("OK(%d,%d,true,%d)", s.id, s.gen, s.lockUntil)
	default:
		n.maxNow = now
		s.gen++
		s.fp = append([]byte(nil), fp...)
		s.state = rActivated
		return fmt.Sprintf("OK(%d,%d,false,%d)", s.id, s.gen, s.lockUntil)
	}
}

func (n *naive) reset(sn []byte, now int64) string {
	if !validRefBytes(sn) || !validRefTime(now) {
		return "Invalid"
	}
	if now < n.maxNow {
		return "ClockBack"
	}
	s := n.sns[string(sn)]
	if s == nil {
		return "Unknown"
	}
	if s.state != rActivated {
		return "State"
	}
	n.maxNow = now
	s.state = rResetPending
	s.fp = nil
	s.e = 0
	s.lockUntil = 0
	return "OK"
}

func (n *naive) deactivate(sn []byte, now int64) string {
	if !validRefBytes(sn) || !validRefTime(now) {
		return "Invalid"
	}
	if now < n.maxNow {
		return "ClockBack"
	}
	s := n.sns[string(sn)]
	if s == nil {
		return "Unknown"
	}
	if s.state != rActivated && s.state != rResetPending {
		return "State"
	}
	n.maxNow = now
	s.state = rRegistered
	s.fp = nil
	n.used[s.tenant]--
	return "OK"
}

type opKind int

const (
	kAddTenant opKind = iota
	kRegister
	kActivate
	kReset
	kDeactivate
)

type op struct {
	kind       opKind
	tenant     string
	quota      int
	until, now int64
	sn, fp     []byte
	sns        [][]byte
	input      string
}

func realErrName(err error) string {
	switch {
	case err == nil:
		return "OK"
	case errors.Is(err, activate.ErrInvalid):
		return "Invalid"
	case errors.Is(err, activate.ErrClockBack):
		return "ClockBack"
	case errors.Is(err, activate.ErrUnknown):
		return "Unknown"
	case errors.Is(err, activate.ErrLocked):
		return "Locked"
	case errors.Is(err, activate.ErrConflict):
		return "Conflict"
	case errors.Is(err, activate.ErrBatchClosed):
		return "BatchClosed"
	case errors.Is(err, activate.ErrQuota):
		return "Quota"
	case errors.Is(err, activate.ErrState):
		return "State"
	case errors.Is(err, roster.ErrUnknownTenant):
		return "UnknownTenant"
	case errors.Is(err, roster.ErrDupSn):
		var d *roster.DupSnError
		errors.As(err, &d)
		if d != nil {
			return fmt.Sprintf("DupSn(%d)", d.Idx)
		}
		return "DupSn"
	default:
		return "UNKNOWN:" + err.Error()
	}
}

func realActName(r activate.ActivateResult, err error) string {
	if err != nil {
		if errors.Is(err, activate.ErrLocked) || errors.Is(err, activate.ErrConflict) {
			return fmt.Sprintf("%s(%d)", realErrName(err), r.LockUntil)
		}
		return realErrName(err)
	}
	return fmt.Sprintf("OK(%d,%d,%t,%d)", r.ID, r.Gen, r.Replayed, r.LockUntil)
}

func strSns(in [][]byte) []string {
	out := make([]string, len(in))
	for i, b := range in {
		out[i] = string(b)
	}
	return out
}

// TestNaiveDifferential1500 随机生成 1500 组操作序列，逐步与朴素模型对照，
// -v 日志打印输入、输出与判定依据。
func TestNaiveDifferential1500(t *testing.T) {
	const groups = 1500
	for seed := int64(0); seed < groups; seed++ {
		rng := rand.New(rand.NewSource(seed))
		m := 1 + int(seed%10)
		lk := int64(1 + seed%1000)
		svc, err := activate.New(m, lk)
		if err != nil {
			t.Fatal(err)
		}
		model := newNaive(int64(m), lk)

		snPool := make([][]byte, 8)
		for i := range snPool {
			snPool[i] = []byte(fmt.Sprintf("S%d", i))
		}
		fpPool := make([][]byte, 4)
		for i := range fpPool {
			fpPool[i] = []byte(fmt.Sprintf("fp%d", i))
		}

		for i := 0; i < 50; i++ {
			o := op{now: int64(i*9) % 500, tenant: fmt.Sprintf("t%d", rng.Intn(3))}
			o.sn = snPool[rng.Intn(len(snPool))]
			o.fp = fpPool[rng.Intn(len(fpPool))]
			if rng.Intn(5) == 0 {
				o.now = int64(rng.Intn(int(o.now) + 1)) // 偶发回退
			}
			switch rng.Intn(10) {
			case 0:
				o.kind = kAddTenant
				o.quota = 1 + rng.Intn(4)
				o.input = fmt.Sprintf("AddTenant(%s,N=%d)", o.tenant, o.quota)
			case 1, 2:
				o.kind = kRegister
				o.until = int64(rng.Intn(500))
				cnt := 1 + rng.Intn(4)
				for j := 0; j < cnt; j++ {
					pick := snPool[rng.Intn(len(snPool))]
					if rng.Intn(6) == 0 && len(o.sns) > 0 {
						pick = o.sns[0] // 故意制造批内重复
					}
					o.sns = append(o.sns, pick)
				}
				o.input = fmt.Sprintf("Register(%s,until=%d,sns=%v,now=%d)", o.tenant, o.until, strSns(o.sns), o.now)
			case 3, 4:
				o.kind = kReset
				o.input = fmt.Sprintf("Reset(%s,now=%d)", o.sn, o.now)
			case 5:
				o.kind = kDeactivate
				o.input = fmt.Sprintf("Deactivate(%s,now=%d)", o.sn, o.now)
			default:
				o.kind = kActivate
				o.input = fmt.Sprintf("Activate(%s,%s,now=%d)", o.sn, o.fp, o.now)
			}

			var got, want string
			switch o.kind {
			case kAddTenant:
				got = realErrName(svc.Roster().AddTenant(o.tenant, o.quota))
				want = model.addTenant(o.tenant, o.quota)
			case kRegister:
				got = realErrName(svc.Roster().RegisterBatch("batch", o.tenant, o.until, o.sns, o.now))
				want = model.registerBatch(o.tenant, o.until, o.sns, o.now)
			case kActivate:
				r, e := svc.Activate(o.sn, o.fp, o.now)
				got = realActName(r, e)
				want = model.activate(o.sn, o.fp, o.now)
			case kReset:
				got = realErrName(svc.Reset(o.sn, o.now))
				want = model.reset(o.sn, o.now)
			case kDeactivate:
				got = realErrName(svc.Deactivate(o.sn, o.now))
				want = model.deactivate(o.sn, o.now)
			}
			judge := "一致"
			if got != want {
				judge = "不一致"
			}
			t.Logf("seed=%04d step=%02d | %-50s | got=%-18s want=%-18s | 判定=%s",
				seed, i, o.input, got, want, judge)
			if got != want {
				t.Fatalf("seed=%d step=%d %s: got=%s want=%s", seed, i, o.input, got, want)
			}
		}
		assertModelState(t, svc, model)
	}
}

func assertModelState(t *testing.T, svc *activate.Service, model *naive) {
	t.Helper()
	realUsed := map[string]int{}
	idSet := map[int64]bool{}
	maxID := int64(0)
	for key, ms := range model.sns {
		rs := svc.Roster().Get([]byte(key))
		if rs == nil {
			t.Fatalf("sn %s missing in real roster", key)
		}
		var wantState roster.State
		switch ms.state {
		case rRegistered:
			wantState = roster.Registered
		case rActivated:
			wantState = roster.Activated
			realUsed[rs.Tenant]++
		case rResetPending:
			wantState = roster.ResetPending
			realUsed[rs.Tenant]++
		}
		if rs.State != wantState || rs.ID != ms.id || rs.Gen != ms.gen || !refFpEq(rs.FP, ms.fp) {
			t.Fatalf("sn %s mismatch: real(state=%d id=%d gen=%d fp=%s) model(state=%d id=%d gen=%d fp=%s)",
				key, rs.State, rs.ID, rs.Gen, rs.FP, ms.state, ms.id, ms.gen, ms.fp)
		}
		gs, _ := svc.Guard().Get([]byte(key))
		if gs.E != ms.e || gs.K != ms.k || gs.LockUntil != ms.lockUntil {
			t.Fatalf("sn %s guard mismatch: real=%+v model(e=%d k=%d lu=%d)",
				key, gs, ms.e, ms.k, ms.lockUntil)
		}
		if rs.ID != 0 {
			idSet[rs.ID] = true
			if rs.ID > maxID {
				maxID = rs.ID
			}
		}
	}
	// id 连续无洞。
	for id := int64(1); id <= maxID; id++ {
		if !idSet[id] {
			t.Fatalf("id %d missing (hole), max=%d", id, maxID)
		}
	}
	// 名额占用与状态计数一致。
	for tenant, u := range realUsed {
		if u > model.quota[tenant] {
			t.Fatalf("tenant %s used=%d over quota=%d", tenant, u, model.quota[tenant])
		}
		if u != model.used[tenant] {
			t.Fatalf("tenant %s realUsed=%d modelUsed=%d", tenant, u, model.used[tenant])
		}
	}
}
