package gate_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/gate"
	"ontology/policy"
)

// naiveSim 按题目规则逐步朴素模拟，不做任何索引优化。
type naiveSim struct {
	ptTenant    map[string]string
	bucketOwner map[string]string
	suspended   map[string]bool

	identity map[string][]policy.Statement
	boundary map[string][]policy.Statement
	bucket   map[string][]policy.BucketStatement
	boundSet map[string]bool

	epoch uint64
}

func newNaiveSim() *naiveSim {
	return &naiveSim{
		ptTenant:    map[string]string{},
		bucketOwner: map[string]string{},
		suspended:   map[string]bool{},
		identity:    map[string][]policy.Statement{},
		boundary:    map[string][]policy.Statement{},
		bucket:      map[string][]policy.BucketStatement{},
		boundSet:    map[string]bool{},
	}
}

func simMatchAction(pat, act string) bool { return pat == policy.ActionAll || pat == act }

func simMatchResource(pat, res string) bool {
	if pat == "*" {
		return true
	}
	if n := len(pat); n > 0 && pat[n-1] == '*' {
		prefix := pat[:n-1]
		return len(res) >= len(prefix) && res[:len(prefix)] == prefix
	}
	return pat == res
}

func simMatchPrincipal(pat, p, t string) bool {
	switch {
	case pat == "*":
		return true
	case len(pat) >= 2 && pat[:2] == "t:":
		return pat[2:] == t
	case len(pat) >= 2 && pat[:2] == "p:":
		return pat[2:] == p
	}
	return false
}

func simStmtHit(s policy.Statement, act, res string) bool {
	ah, rh := false, false
	for _, a := range s.Actions {
		if simMatchAction(a, act) {
			ah = true
		}
	}
	for _, r := range s.Resources {
		if simMatchResource(r, res) {
			rh = true
		}
	}
	return ah && rh
}

func simBktHit(s policy.BucketStatement, p, t, act, res string) bool {
	ph := false
	for _, q := range s.Principals {
		if simMatchPrincipal(q, p, t) {
			ph = true
		}
	}
	return ph && simStmtHit(s.Statement, act, res)
}

func (m *naiveSim) check(p, act, bucket, res string) (gate.Reason, string) {
	if !policy.IsAction(act) || (act != policy.ActionList && res == "") {
		return gate.ReasonBadRequest, "动作非法或非 List 资源为空"
	}
	t, ok := m.ptTenant[p]
	if !ok {
		return gate.ReasonBadRequest, fmt.Sprintf("主体 %s 未登记", p)
	}
	owner, ok := m.bucketOwner[bucket]
	if !ok {
		return gate.ReasonNoBucket, fmt.Sprintf("桶 %s 不存在", bucket)
	}
	if m.suspended[t] {
		return gate.ReasonTenantSusp, fmt.Sprintf("主体租户 %s 已暂停", t)
	}
	if m.suspended[owner] && t != owner && (act == policy.ActionPut || act == policy.ActionDelete) {
		return gate.ReasonFrozen, fmt.Sprintf("属主租户 %s 暂停，写冻结", owner)
	}

	idDeny, idAllow := false, false
	for _, s := range m.identity[p] {
		if simStmtHit(s, act, res) {
			if s.Effect == policy.Deny {
				idDeny = true
			} else {
				idAllow = true
			}
		}
	}
	bdDeny, bdAllow := false, false
	for _, s := range m.boundary[p] {
		if simStmtHit(s, act, res) {
			if s.Effect == policy.Deny {
				bdDeny = true
			} else {
				bdAllow = true
			}
		}
	}
	bkDeny, bkAllow := false, false
	for _, s := range m.bucket[bucket] {
		if simBktHit(s, p, t, act, res) {
			if s.Effect == policy.Deny {
				bkDeny = true
			} else {
				bkAllow = true
			}
		}
	}
	if idDeny || bdDeny || bkDeny {
		return gate.ReasonDeny, fmt.Sprintf(
			"命中 Deny(id=%v,boundary=%v,bucket=%v)", idDeny, bdDeny, bkDeny)
	}
	if m.boundSet[p] && !bdAllow {
		return gate.ReasonOutOfBound, "设置了边界但无边界 Allow 命中"
	}
	if t == owner {
		if !idAllow && !bkAllow {
			return gate.ReasonNoPermit, fmt.Sprintf("同租户无身份/桶 Allow(id=%v,bucket=%v)", idAllow, bkAllow)
		}
		return gate.ReasonOK, fmt.Sprintf("同租户并集允许(id=%v,bucket=%v)", idAllow, bkAllow)
	}
	if !idAllow {
		return gate.ReasonNoIdentity, "跨租户缺身份 Allow"
	}
	if !bkAllow {
		return gate.ReasonNoResource, fmt.Sprintf("跨租户缺桶 Allow(owner=%s)", owner)
	}
	return gate.ReasonOK, "跨租户交集允许"
}

func errClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, gate.ErrNotFound):
		return "notfound"
	case errors.Is(err, gate.ErrAlreadyExists):
		return "exists"
	case errors.Is(err, gate.ErrBadRequest):
		return "badrequest"
	case errors.Is(err, gate.ErrBadPolicy):
		return "badpolicy"
	case errors.Is(err, gate.ErrTooManyStatements):
		return "toomany"
	}
	return err.Error()
}

func randomStmts(rng *rand.Rand, resPats []string, pickActs func() []string) []policy.Statement {
	n := rng.Intn(4)
	out := make([]policy.Statement, n)
	for i := range out {
		out[i] = randomStmt(rng, resPats, pickActs)
	}
	return out
}

func randomStmt(rng *rand.Rand, resPats []string, pickActs func() []string) policy.Statement {
	eff := policy.Allow
	if rng.Intn(3) == 0 {
		eff = policy.Deny
	}
	nres := 1 + rng.Intn(2)
	ress := make([]string, nres)
	for j := range ress {
		ress[j] = resPats[rng.Intn(len(resPats))]
	}
	return policy.Statement{Effect: eff, Actions: pickActs(), Resources: ress}
}

// TestRandomAgainstNaiveSim 1500 次随机请求与朴素模拟逐条对照（含随机整份替换/暂停）。
func TestRandomAgainstNaiveSim(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	g := gate.New()
	m := newNaiveSim()

	tenants := []string{"A", "B", "C"}
	principals := []string{}
	for _, tn := range tenants {
		for _, u := range []string{"u1", "u2"} {
			principals = append(principals, tn+"/"+u)
		}
	}
	buckets := []string{"bA", "bB", "bC"}
	ownerOf := map[string]string{"bA": "A", "bB": "B", "bC": "C"}
	resources := []string{"", "a", "a/", "a/x", "ab", "pub/x", "pub/ab", "priv/x",
		"pub/keep1", "blocked", "k", "secret/x"}
	actions := []string{policy.ActionGet, policy.ActionList, policy.ActionPut, policy.ActionDelete}
	resPats := []string{"*", "", "a/*", "a", "pub/*", "pub/a*", "priv/*", "pub/keep*",
		"block*", "secret*", "k", "x*"}
	prinPats := []string{"*", "t:A", "t:B", "t:C"}
	for _, p := range principals {
		prinPats = append(prinPats, "p:"+p)
	}
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	pickActs := func() []string {
		n := 1 + rng.Intn(2)
		all := append(append([]string{}, actions...), policy.ActionAll)
		out := make([]string, n)
		for i := range out {
			out[i] = all[rng.Intn(len(all))]
		}
		return out
	}

	for _, p := range principals {
		if err := g.RegisterPrincipal(p, p[:1]); err != nil {
			t.Fatal(err)
		}
		m.ptTenant[p] = p[:1]
	}
	for _, b := range buckets {
		if err := g.CreateBucket(ownerOf[b], b); err != nil {
			t.Fatal(err)
		}
		m.bucketOwner[b] = ownerOf[b]
	}

	checks := 0
	for checks < 1500 {
		switch rng.Intn(6) {
		case 0, 1, 2: // Check
			p := pick(principals)
			act := pick(actions)
			b := pick(buckets)
			var res string
			if act == policy.ActionList {
				res = pick(resources)
			} else {
				res = resources[1+rng.Intn(len(resources)-1)]
			}
			d := g.Check(p, act, b, res)
			want, basis := m.check(p, act, b, res)
			t.Logf("Check #%d 输入={p:%s act:%s bucket:%s res:%q} 输出=%s epoch=%d | 模拟=%s 依据:%s",
				checks, p, act, b, res, d.Reason, d.Epoch, want, basis)
			if d.Reason != want {
				t.Fatalf("mismatch check %d: got %s want %s (p=%s act=%s b=%s res=%q)",
					checks, d.Reason, want, p, act, b, res)
			}
			if d.Epoch != m.epoch {
				t.Fatalf("epoch mismatch: got %d want %d", d.Epoch, m.epoch)
			}
			checks++
		case 3: // 身份策略整份替换
			p := pick(principals)
			stmts := randomStmts(rng, resPats, pickActs)
			err := g.SetIdentityPolicy(p, stmts)
			if err == nil {
				m.identity[p] = append([]policy.Statement(nil), stmts...)
				m.epoch++
			}
			t.Logf("SetIdentityPolicy p=%s n=%d -> %s (epoch=%d)", p, len(stmts), errClass(err), m.epoch)
		case 4: // 边界整份替换（约 1/5 设为空列表）
			p := pick(principals)
			var stmts []policy.Statement
			if rng.Intn(5) != 0 {
				stmts = randomStmts(rng, resPats, pickActs)
			}
			err := g.SetBoundary(p, stmts)
			if err == nil {
				m.boundary[p] = append([]policy.Statement(nil), stmts...)
				m.boundSet[p] = true
				m.epoch++
			}
			t.Logf("SetBoundary p=%s n=%d -> %s (epoch=%d)", p, len(stmts), errClass(err), m.epoch)
		case 5: // 桶策略整份替换
			b := pick(buckets)
			n := rng.Intn(4)
			stmts := make([]policy.BucketStatement, n)
			for i := range stmts {
				stmts[i].Statement = randomStmt(rng, resPats, pickActs)
				stmts[i].Principals = []string{pick(prinPats)}
				if rng.Intn(4) == 0 { // 多主体模式
					stmts[i].Principals = append(stmts[i].Principals, pick(prinPats))
				}
			}
			err := g.SetBucketPolicy(b, stmts)
			if err == nil {
				m.bucket[b] = append([]policy.BucketStatement(nil), stmts...)
				m.epoch++
			}
			t.Logf("SetBucketPolicy b=%s n=%d -> %s (epoch=%d)", b, len(stmts), errClass(err), m.epoch)
		}

		if rng.Intn(8) == 0 {
			tn := pick(tenants)
			if rng.Intn(2) == 0 {
				_ = g.Suspend(tn)
				m.suspended[tn] = true
				t.Logf("Suspend %s", tn)
			} else {
				_ = g.Resume(tn)
				delete(m.suspended, tn)
				t.Logf("Resume %s", tn)
			}
		}
	}
	t.Logf("完成 1500 组随机对照，最终 epoch=%d", m.epoch)
}

// TestConcurrentEpochConsistency 并发整份替换与 Check：每次 Check 只见单一纪元的完整集合。
func TestConcurrentEpochConsistency(t *testing.T) {
	g := gate.New()
	must(t, g.RegisterPrincipal("p", "T"))
	must(t, g.CreateBucket("T", "b"))

	polAllow := []policy.Statement{stAllow([]string{policy.ActionGet}, []string{"*"})}
	polDeny := []policy.Statement{stDeny([]string{policy.ActionGet}, []string{"*"})}
	must(t, g.SetIdentityPolicy("p", polDeny)) // epoch 1（奇数）：Deny；之后 Allow/Deny 成对替换

	start := make(chan struct{})
	var wg sync.WaitGroup
	const writerSets, readers = 1200, 8

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < writerSets; i++ {
			_ = g.SetIdentityPolicy("p", polAllow) // 偶数纪元：Allow
			_ = g.SetIdentityPolicy("p", polDeny)  // 奇数纪元：Deny
		}
	}()

	var mu sync.Mutex
	seen := map[gate.Reason]int{}
	epochs := map[uint64]bool{}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 2000; i++ {
				d := g.Check("p", policy.ActionGet, "b", "k")
				// 纪元奇偶与结果必须一致：奇数纪元为 Deny 策略，偶数纪元为 Allow 策略。
				if d.Epoch%2 == 1 && d.Reason != gate.ReasonDeny {
					t.Errorf("odd epoch %d but reason %s (want Deny)", d.Epoch, d.Reason)
					return
				}
				if d.Epoch%2 == 0 && d.Reason != gate.ReasonOK {
					t.Errorf("even epoch %d but reason %s (want Allow)", d.Epoch, d.Reason)
					return
				}
				mu.Lock()
				seen[d.Reason]++
				epochs[d.Epoch] = true
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	t.Logf("并发 Check 结果分布=%v，观察到 %d 个不同纪元（终态 epoch=%d）",
		seen, len(epochs), g.Epoch())
	if g.Epoch() != 1+uint64(writerSets)*2 {
		t.Fatalf("lost update: epoch=%d want %d", g.Epoch(), 1+writerSets*2)
	}
}

// TestProbesIndexing probes 只统计动作模式能匹配所查动作的语句：
// 无关动作语句为 10 与 10000 两档时，同一次 Check 的 probes 相同。
func TestProbesIndexing(t *testing.T) {
	measure := func(t *testing.T, noise int) uint64 {
		t.Helper()
		g := gate.New()
		must(t, g.RegisterPrincipal("p", "T"))
		must(t, g.CreateBucket("T", "b"))
		must(t, g.SetIdentityPolicy("p", []policy.Statement{
			stAllow([]string{policy.ActionGet}, []string{"*"}),
		}))
		must(t, g.SetBucketPolicy("b", []policy.BucketStatement{
			bkAllow([]string{"*"}, []string{policy.ActionGet}, []string{"*"}),
		}))
		// 噪声：其他主体策略中仅含写动作的语句（朴素全表扫描会触达，索引不会）。
		// 每份至多 200 条，故分散到多个主体。
		const perDoc = 50
		for placed, idx := 0, 0; placed < noise; idx++ {
			np := fmt.Sprintf("n/%d", idx)
			must(t, g.RegisterPrincipal(np, "N"))
			n := perDoc
			if noise-placed < n {
				n = noise - placed
			}
			noisePol := make([]policy.Statement, n)
			for i := range noisePol {
				noisePol[i] = stAllow([]string{policy.ActionPut, policy.ActionDelete}, []string{"*"})
			}
			must(t, g.SetIdentityPolicy(np, noisePol))
			placed += n
		}
		g.ResetProbes()
		d := g.Check("p", policy.ActionGet, "b", "k")
		if d.Reason != gate.ReasonOK {
			t.Fatalf("unexpected reason %s", d.Reason)
		}
		return g.ProbeCount()
	}

	p10 := measure(t, 10)
	p10000 := measure(t, 10000)
	t.Logf("噪声 10 时 probes=%d；噪声 10000 时 probes=%d", p10, p10000)
	if p10 != p10000 {
		t.Fatalf("probes changed with irrelevant statements: %d != %d", p10, p10000)
	}
	if p10 == 0 {
		t.Fatalf("probes unexpectedly zero")
	}
}
