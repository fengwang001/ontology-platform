package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/ledger"
	"ontology/policy"
)

var simNames = []string{"alice", "bob", "carol", "dave"}
var simResources = []string{"/db", "/db/prod", "/d", "/app"}

func TestModelCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	p0 := policy.Policy{Dmax: 50, Rw: 20, Lk: 10, Cmax: 2}
	svc, err := New(p0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m := newModel(p0)
	roles := []policy.Role{policy.Engineer, policy.Manager, policy.Security}
	now := int64(0)
	for i := 0; i < 300; i++ {
		now += int64(rng.Intn(5))
		if rng.Intn(10) == 0 {
			now -= int64(rng.Intn(10)) // 制造时钟回退
		}
		if now < 0 {
			now = 0
		}
		var svcErr, mErr error
		var desc string
		switch rng.Intn(5) {
		case 0:
			name := simNames[rng.Intn(len(simNames))]
			role := roles[rng.Intn(len(roles))]
			var scopes []string
			if role == policy.Manager && rng.Intn(2) == 0 {
				scopes = []string{simResources[rng.Intn(len(simResources))]}
			}
			desc = fmt.Sprintf("Register(%d,%s,%s,%v)", now, name, role, scopes)
			svcErr, mErr = svc.Register(now, name, role, scopes), m.register(now, name, role, scopes)
		case 1:
			p := policy.Policy{Dmax: int64(1 + rng.Intn(60)), Rw: int64(1 + rng.Intn(30)),
				Lk: int64(rng.Intn(20)), Cmax: int64(1 + rng.Intn(3))}
			desc = fmt.Sprintf("SetPolicy(%d,%+v)", now, p)
			svcErr, mErr = svc.SetPolicy(now, p), m.setPolicy(now, p)
		case 2:
			id := fmt.Sprintf("g%d", rng.Intn(40))
			req := simNames[rng.Intn(len(simNames))]
			res := simResources[rng.Intn(len(simResources))]
			d := int64(1 + rng.Intn(60))
			desc = fmt.Sprintf("Request(%d,%s,%s,%s,%d)", now, id, req, res, d)
			svcErr, mErr = svc.Request(now, id, req, res, d), m.request(now, id, req, res, d)
		default:
			id := fmt.Sprintf("g%d", rng.Intn(40))
			ap := simNames[rng.Intn(len(simNames))]
			approve := rng.Intn(2) == 0
			desc = fmt.Sprintf("review(%d,%s,%s,approve=%v)", now, id, ap, approve)
			svcErr, mErr = svc.review(now, id, ap, approve), m.review(now, id, ap, approve)
		}
		t.Logf("op %d: %s => svc=%v model=%v", i, desc, svcErr, mErr)
		if svcErr != mErr {
			t.Fatalf("op %d %s: svc=%v model=%v", i, desc, svcErr, mErr)
		}
		p := simNames[rng.Intn(len(simNames))]
		res := simResources[rng.Intn(len(simResources))]
		at := int64(0)
		if c := svc.Clock(); c > 0 {
			at = int64(rng.Intn(int(c) + 1))
		}
		got, err := svc.Access(p, res, at)
		if err != nil {
			t.Fatalf("Access: %v", err)
		}
		want := m.access(p, res, at)
		t.Logf("access(%s,%s,%d) => svc=%v model=%v (依据: 逐步重算 effEnd)", p, res, at, got, want)
		if got != want {
			t.Fatalf("Access(%s,%s,%d): svc=%v model=%v", p, res, at, got, want)
		}
	}
}

func TestReplayDeterministic(t *testing.T) {
	script := func(s *Service) {
		_ = s.Register(0, "alice", policy.Engineer, nil)
		_ = s.Register(1, "m1", policy.Manager, []string{"/db"})
		_ = s.SetPolicy(2, policy.Policy{Dmax: 80, Rw: 25, Lk: 5, Cmax: 2})
		_ = s.Request(3, "g1", "alice", "/db/prod", 40)
		_ = s.Approve(10, "g1", "m1")
		_ = s.Request(11, "g2", "alice", "/db", 20)
		_ = s.Reject(12, "g2", "m1")
	}
	run := func() []ledger.Entry {
		s, err := New(policy.Policy{Dmax: 100, Rw: 30, Lk: 50, Cmax: 3})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		script(s)
		return s.Ledger(0)
	}
	a, b := run(), run()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("重放账本不一致:\n%v\n%v", a, b)
	}
	if len(a) != 7 {
		t.Fatalf("ledger len = %d, want 7", len(a))
	}
	for i, e := range a {
		if e.Seq != int64(i+1) {
			t.Fatalf("Seq 不连续: entry %d = %+v", i, e)
		}
	}
	t.Logf("重放账本逐条相同, 共 %d 条, Seq 1..%d 无洞", len(a), len(a))
}

func TestRejectedOpsNotInLedger(t *testing.T) {
	s := newSvc(t)
	setupExample(t, s)
	before := s.Ledger(0)
	clock := s.Clock()
	_ = s.Register(5, "", policy.Engineer, nil)      // 参数非法
	_ = s.Register(2, "alice", policy.Engineer, nil) // 时钟回退
	_ = s.SetPolicy(5, policy.Policy{})              // 参数非法
	_ = s.Request(5, "", "alice", "/db", 10)         // 参数非法
	_ = s.Request(5, "g1", "ghost", "/db", 10)       // 未登记
	_ = s.Approve(5, "nope", "m1")                   // 授权不存在
	after := s.Ledger(0)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("被拒绝的操作写入了账本: %v -> %v", before, after)
	}
	if s.Clock() != clock {
		t.Fatalf("被拒绝的操作推进了时钟: %d -> %d", clock, s.Clock())
	}
	for i, e := range after {
		if e.Seq != int64(i+1) {
			t.Fatalf("Seq 不连续: entry %d = %+v", i, e)
		}
	}
	if got := len(s.Ledger(2)); got != len(after)-2 {
		t.Fatalf("Ledger(2) 返回 %d 条, want %d", got, len(after)-2)
	}
}
