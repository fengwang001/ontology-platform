package gate

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/policy"
)

// naiveDecision 是不建索引、逐条扫描的朴素判定结果。
type naiveDecision struct {
	allowed bool
	reason  string
	epoch   int
	basis   string
}

// naiveSim 独立按题面规则重放整份操作序列，不依赖 policy.Store 的索引。
type naiveSim struct {
	principals map[string]string
	buckets    map[string]string
	suspended  map[string]bool
	identity   map[string][]policy.Statement
	boundSet   map[string]bool
	boundary   map[string][]policy.Statement
	bucketPol  map[string][]policy.BucketStatement
	epoch      int
}

func newNaive() *naiveSim {
	return &naiveSim{
		principals: map[string]string{},
		buckets:    map[string]string{},
		suspended:  map[string]bool{},
		identity:   map[string][]policy.Statement{},
		boundSet:   map[string]bool{},
		boundary:   map[string][]policy.Statement{},
		bucketPol:  map[string][]policy.BucketStatement{},
	}
}

func (n *naiveSim) known(t string) bool {
	for _, x := range n.principals {
		if x == t {
			return true
		}
	}
	for _, x := range n.buckets {
		if x == t {
			return true
		}
	}
	return false
}

func naiveMatchAction(pat, act string) bool { return pat == "obj:*" || pat == act }

func naiveMatchResource(pat, res string) bool {
	switch {
	case pat == "*":
		return true
	case strings.HasSuffix(pat, "*"):
		return strings.HasPrefix(res, strings.TrimSuffix(pat, "*"))
	default:
		return pat == res
	}
}

func naiveMatchPrincipal(pat, p, t string) bool {
	switch {
	case pat == "*":
		return true
	case strings.HasPrefix(pat, "t:"):
		return pat[2:] == t
	default:
		return strings.HasPrefix(pat, "p:") && pat[2:] == p
	}
}

func validAction(a string) bool {
	switch a {
	case "obj:Get", "obj:List", "obj:Put", "obj:Delete":
		return true
	}
	return false
}

func isWrite(a string) bool { return a == "obj:Put" || a == "obj:Delete" }

func naiveStmtHit(s policy.Statement, act, res string) bool {
	am := false
	for _, a := range s.Actions {
		if naiveMatchAction(a, act) {
			am = true
		}
	}
	if !am {
		return false
	}
	for _, r := range s.Resources {
		if naiveMatchResource(r, res) {
			return true
		}
	}
	return false
}

func naiveBktHit(s policy.BucketStatement, p, t, act, res string) bool {
	if !naiveStmtHit(s.Statement, act, res) {
		return false
	}
	for _, pat := range s.Principals {
		if naiveMatchPrincipal(pat, p, t) {
			return true
		}
	}
	return false
}

func (n *naiveSim) check(p, act, bucket, res string) naiveDecision {
	if !validAction(act) || (res == "" && act != "obj:List") {
		return naiveDecision{reason: ReasonInvalid, basis: "参数非法"}
	}
	t, ok := n.principals[p]
	if !ok {
		return naiveDecision{reason: ReasonInvalid, basis: "主体未登记"}
	}
	owner, ok := n.buckets[bucket]
	if !ok {
		return naiveDecision{reason: ReasonNoBucket, basis: "桶不存在"}
	}
	ep := n.epoch
	if n.suspended[t] {
		return naiveDecision{reason: ReasonTenantPaused, epoch: ep, basis: "租户暂停"}
	}
	if n.suspended[owner] && isWrite(act) {
		return naiveDecision{reason: ReasonBucketFrozen, epoch: ep, basis: "桶冻结"}
	}
	for _, s := range n.identity[p] {
		if s.Effect == "Deny" && naiveStmtHit(s, act, res) {
			return naiveDecision{reason: ReasonDeny, epoch: ep, basis: "身份 Deny"}
		}
	}
	if n.boundSet[p] {
		for _, s := range n.boundary[p] {
			if s.Effect == "Deny" && naiveStmtHit(s, act, res) {
				return naiveDecision{reason: ReasonDeny, epoch: ep, basis: "边界 Deny"}
			}
		}
	}
	for _, s := range n.bucketPol[bucket] {
		if s.Effect == "Deny" && naiveBktHit(s, p, t, act, res) {
			return naiveDecision{reason: ReasonDeny, epoch: ep, basis: "桶 Deny"}
		}
	}
	bndAllow := false
	if n.boundSet[p] {
		for _, s := range n.boundary[p] {
			if s.Effect == "Allow" && naiveStmtHit(s, act, res) {
				bndAllow = true
			}
		}
		if !bndAllow {
			return naiveDecision{reason: ReasonOutOfBoundary, epoch: ep, basis: "越出边界"}
		}
	}
	idAllow := false
	for _, s := range n.identity[p] {
		if s.Effect == "Allow" && naiveStmtHit(s, act, res) {
			idAllow = true
		}
	}
	bktAllow := false
	for _, s := range n.bucketPol[bucket] {
		if s.Effect == "Allow" && naiveBktHit(s, p, t, act, res) {
			bktAllow = true
		}
	}
	if t == owner {
		if idAllow || bktAllow {
			return naiveDecision{allowed: true, epoch: ep, basis: fmt.Sprintf("同租户并集 id=%v bkt=%v", idAllow, bktAllow)}
		}
		return naiveDecision{reason: ReasonNoPermission, epoch: ep, basis: "无许可"}
	}
	if !idAllow {
		return naiveDecision{reason: ReasonNoIdentity, epoch: ep, basis: "缺身份许可"}
	}
	if !bktAllow {
		return naiveDecision{reason: ReasonNoResource, epoch: ep, basis: "缺资源许可"}
	}
	return naiveDecision{allowed: true, epoch: ep, basis: "跨租户交集"}
}

func TestNaiveDifferential1500(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	g := New()
	n := newNaive()

	tenants := []string{"A", "B"}
	principals := []string{"A/v", "B/u"}
	buckets := []string{"dA", "dB"}

	reg := func(p, ten string) {
		err1 := g.RegisterPrincipal(p, ten)
		if _, exists := n.principals[p]; !exists {
			n.principals[p] = ten
		}
		_ = err1
	}
	mkbkt := func(ten, b string) {
		_ = g.CreateBucket(ten, b)
		if _, exists := n.buckets[b]; !exists {
			n.buckets[b] = ten
		}
	}
	reg("A/v", "A")
	reg("B/u", "B")
	mkbkt("A", "dA")
	mkbkt("B", "dB")

	acts := []string{"obj:Get", "obj:List", "obj:Put", "obj:Delete", "obj:*"}
	resPats := []string{"*", "", "a", "a/*", "a*", "pub/*", "pub/x", "x"}
	pPats := []string{"*", "t:A", "t:B", "p:A/v", "p:B/u"}
	checkActs := []string{"obj:Get", "obj:List", "obj:Put", "obj:Delete"}
	checkRes := []string{"", "a", "a/", "a/x", "pub/x", "pub", "x", "pub/ab"}

	randStmt := func() policy.Statement {
		eff := "Allow"
		if rng.Intn(4) == 0 {
			eff = "Deny"
		}
		na := 1 + rng.Intn(2)
		nr := 1 + rng.Intn(2)
		s := policy.Statement{Effect: eff}
		for i := 0; i < na; i++ {
			s.Actions = append(s.Actions, acts[rng.Intn(len(acts))])
		}
		for i := 0; i < nr; i++ {
			s.Resources = append(s.Resources, resPats[rng.Intn(len(resPats))])
		}
		return s
	}
	randStmts := func() []policy.Statement {
		k := rng.Intn(4)
		out := make([]policy.Statement, k)
		for i := range out {
			out[i] = randStmt()
		}
		return out
	}
	randBktStmts := func() []policy.BucketStatement {
		k := rng.Intn(4)
		out := make([]policy.BucketStatement, k)
		for i := range out {
			s := randStmt()
			out[i] = policy.BucketStatement{
				Statement:  s,
				Principals: []string{pPats[rng.Intn(len(pPats))]},
			}
		}
		return out
	}

	suspend := func(ten string, resume bool) {
		var err error
		if resume {
			err = g.Resume(ten)
		} else {
			err = g.Suspend(ten)
		}
		if n.known(ten) {
			if err != nil {
				t.Fatalf("naive knows %s but gateway err %v", ten, err)
			}
			if resume {
				delete(n.suspended, ten)
			} else {
				n.suspended[ten] = true
			}
		} else if err == nil {
			t.Fatalf("naive does not know %s but gateway accepted", ten)
		}
	}

	checks := 0
	for round := 0; round < 1500; round++ {
		// 每轮以约 2/3 概率先做一次变更，再必定发起一次 Check，保证 1500 组逐步对照。
		switch rng.Intn(9) {
		case 0, 1:
			p := principals[rng.Intn(2)]
			stmts := randStmts()
			err := g.SetIdentityPolicy(p, stmts)
			if err == nil {
				n.identity[p] = stmts
				n.epoch++
			} else if err != policy.ErrInvalid && err != policy.ErrTooMany {
				t.Fatalf("unexpected set identity err %v", err)
			}
		case 2:
			p := principals[rng.Intn(2)]
			stmts := randStmts()
			err := g.SetBoundary(p, stmts)
			if err == nil {
				n.boundSet[p] = true
				n.boundary[p] = stmts
				n.epoch++
			} else if err != policy.ErrInvalid && err != policy.ErrTooMany {
				t.Fatalf("unexpected boundary err %v", err)
			}
		case 3:
			b := buckets[rng.Intn(2)]
			stmts := randBktStmts()
			err := g.SetBucketPolicy(b, stmts)
			if err == nil {
				n.bucketPol[b] = stmts
				n.epoch++
			} else if err != policy.ErrInvalid && err != policy.ErrTooMany {
				t.Fatalf("unexpected bucket err %v", err)
			}
		case 4:
			suspend(tenants[rng.Intn(2)], rng.Intn(2) == 0)
		}
		p := principals[rng.Intn(2)]
		if rng.Intn(10) == 0 {
			p = "ghost"
		}
		act := checkActs[rng.Intn(4)]
		b := buckets[rng.Intn(2)]
		if rng.Intn(10) == 0 {
			b = "nobucket"
		}
		res := checkRes[rng.Intn(len(checkRes))]
		if rng.Intn(15) == 0 {
			act = "obj:Bogus"
		}
		got := g.Check(p, act, b, res)
		want := n.check(p, act, b, res)
		t.Logf("round=%d Check(%q,%q,%q,%q) => allowed=%v reason=%q epoch=%d basis=%q | naive: %q ep=%d (%s)",
			round, p, act, b, res, got.Allowed, got.Reason, got.Epoch, got.Basis,
			want.reason, want.epoch, want.basis)
		if got.Allowed != want.allowed || got.Reason != want.reason || got.Epoch != want.epoch {
			t.Fatalf("mismatch round=%d input=(%s,%s,%s,%s)\n got={%v %s ep=%d %s}\nwant={%v %s ep=%d %s}",
				round, p, act, b, res,
				got.Allowed, got.Reason, got.Epoch, got.Basis,
				want.allowed, want.reason, want.epoch, want.basis)
		}
		checks++
	}
	t.Logf("differential checks compared: %d, final epoch=%d", checks, n.epoch)
}
