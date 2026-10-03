package gate

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/introspect"
	"ontology/scope"
)

// simEntry 是朴素模型的一条缓存记录（逐条按规格手算）。
type simEntry struct {
	res   introspect.Result
	f0, u int64
}

// naiveModel 完全按题述规则逐步重放：P/N/G 与真实网关一致。
type naiveModel struct {
	P, N, G int64
	rec     map[string]simEntry
	nb      map[string]int64
	maxNow  int64
	calls   int64
	// 与真实 f 共享的按令牌脚本
	script map[string][]introspect.Result
	errs   map[string][]error
	next   map[string]int
}

func newNaive(P, N, G int64, script map[string][]introspect.Result, errs map[string][]error) *naiveModel {
	return &naiveModel{P: P, N: N, G: G, rec: map[string]simEntry{}, nb: map[string]int64{},
		script: script, errs: errs, next: map[string]int{}}
}

func (m *naiveModel) callF(token string) (introspect.Result, error) {
	i := m.next[token]
	m.next[token] = i + 1
	m.calls++
	return m.script[token][i], m.errs[token][i]
}

type simResult struct {
	verdict Verdict
	src     string
	missing string
	err     error
	calls   int64
}

func (m *naiveModel) check(token string, need []string, now int64) simResult {
	if token == "" || len(need) == 0 {
		return simResult{err: ErrInvalid, calls: m.calls}
	}
	for _, n := range need {
		if n == "" {
			return simResult{err: ErrInvalid, calls: m.calls}
		}
	}
	if now < 0 || now > maxTime {
		return simResult{err: ErrTime, calls: m.calls}
	}
	if now < m.maxNow {
		return simResult{err: ErrClock, calls: m.calls}
	}
	m.maxNow = now // 任何通过校验的调用都推进水位

	var e simEntry
	src := "None"
	if c, ok := m.rec[token]; ok && now < c.u {
		e, src = c, "Cache"
	} else if c, ok := m.rec[token]; ok && c.res.Active {
		if nbSub, revoked := m.nb[c.res.Sub]; revoked && c.res.Iat <= nbSub {
			e, src = c, "Cache"
		} else {
			res, ferr := m.callF(token)
			if ferr == nil {
				u := int64(0)
				if res.Active {
					u = now + m.P
					if res.Exp < u {
						u = res.Exp
					}
				} else {
					u = now + m.N
				}
				e = simEntry{res: res, f0: now, u: u}
				m.rec[token] = e
				src = "Fresh"
			} else {
				old, ok2 := m.rec[token]
				ok2 = ok2 && old.res.Active && !scope.HighRisk(need) &&
					old.u <= now && now < old.u+m.G && now < old.res.Exp
				if !ok2 {
					return simResult{verdict: Unavailable, src: "None", calls: m.calls}
				}
				e, src = old, "Stale"
			}
		}
	} else {
		res, ferr := m.callF(token)
		if ferr == nil {
			u := int64(0)
			if res.Active {
				u = now + m.P
				if res.Exp < u {
					u = res.Exp
				}
			} else {
				u = now + m.N
			}
			e = simEntry{res: res, f0: now, u: u}
			m.rec[token] = e
			src = "Fresh"
		} else {
			c2, ok2 := m.rec[token]
			ok2 = ok2 && c2.res.Active && !scope.HighRisk(need) &&
				c2.u <= now && now < c2.u+m.G && now < c2.res.Exp
			if !ok2 {
				return simResult{verdict: Unavailable, src: "None", calls: m.calls}
			}
			e, src = c2, "Stale"
		}
	}

	nb := int64(-1)
	if v, ok := m.nb[e.res.Sub]; ok {
		nb = v
	}
	r := simResult{src: src, calls: m.calls}
	switch {
	case !e.res.Active:
		r.verdict = Inactive
	case e.res.Iat <= nb:
		r.verdict = Revoked
	case now >= e.res.Exp:
		r.verdict = Expired
	default:
		if miss := scope.FirstMissing(e.res.Scopes, need); miss != "" {
			r.verdict, r.missing = Scope, miss
		} else {
			r.verdict = Allow
		}
	}
	return r
}

func (m *naiveModel) revoke(sub string, t, now int64) error {
	if sub == "" || t < 0 || t > maxTime {
		return ErrInvalid
	}
	if now < 0 || now > maxTime {
		return ErrTime
	}
	if now < m.maxNow {
		return ErrClock
	}
	m.maxNow = now
	if v, ok := m.nb[sub]; !ok || t > v {
		m.nb[sub] = t
	}
	return nil
}

// 随机空间定义
var simScopes = []string{"orders", "orders:read", "orders:write", "orders:read:x", "orders:write:x", "billing", "billing:read", "billing:admin", "admin"}
var simSubs = []string{"u1", "u2", "u3"}
var simNeeds = [][]string{
	{"orders"}, {"orders:read"}, {"orders:write"}, {"orders:write:x"},
	{"orders:read", "billing:read"}, {"billing:admin"}, {"ordersx"},
}

func genWorld(rng *rand.Rand) (map[string][]introspect.Result, map[string][]error, []string) {
	tokens := []string{"t1", "t2", "t3", "t4"}
	script := map[string][]introspect.Result{}
	errs := map[string][]error{}
	for _, tok := range tokens {
		k := 50
		for i := 0; i < k; i++ {
			if rng.Intn(5) == 0 { // 20% 上游错误
				errs[tok] = append(errs[tok], errBoom)
				script[tok] = append(script[tok], introspect.Result{})
				continue
			}
			if rng.Intn(6) == 0 { // 部分负向记录
				errs[tok] = append(errs[tok], nil)
				script[tok] = append(script[tok], introspect.Result{Active: false})
				continue
			}
			sub := simSubs[rng.Intn(len(simSubs))]
			iat := int64(rng.Intn(8))
			exp := int64(20 + rng.Intn(120))
			if rng.Intn(4) == 0 {
				exp = int64(rng.Intn(30)) // 有时 exp 很早
			}
			nsc := 1 + rng.Intn(3)
			sc := make([]string, nsc)
			for j := range sc {
				sc[j] = simScopes[rng.Intn(len(simScopes))]
			}
			errs[tok] = append(errs[tok], nil)
			script[tok] = append(script[tok], introspect.Result{Active: true, Sub: sub, Iat: iat, Exp: exp, Scopes: sc})
		}
	}
	return script, errs, tokens
}

// 以下计数器仅用于让真实 f 与朴素模型共享同一脚本；测试为串行执行。
var consumed = map[string]int{}

func countConsumed(token string) int { return consumed[token] }
func recordConsume(token string)     { consumed[token]++ }

func TestRandomAgainstNaive(t *testing.T) {
	const groups = 2000
	for seed := int64(1); seed <= groups; seed++ {
		rng := rand.New(rand.NewSource(seed))
		script, errs, tokens := genWorld(rng)
		consumed = map[string]int{}

		// 真实上游：按令牌消费同一脚本，并计数
		var realCalls int64
		f := func(token string) (introspect.Result, error) {
			realCalls++
			i := countConsumed(token)
			recordConsume(token)
			return script[token][i], errs[token][i]
		}
		g := New(10, 5, 20, f)
		m := newNaive(10, 5, 20, script, errs)

		var log []string
		failf := func(format string, args ...any) {
			for _, l := range log {
				t.Log(l)
			}
			t.Fatalf("seed=%d: %s", seed, fmt.Sprintf(format, args...))
		}

		now := int64(0)
		for step := 0; step < 40; step++ {
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4, 5, 6, 7: // 80% Check
				tok := tokens[rng.Intn(len(tokens))]
				need := simNeeds[rng.Intn(len(simNeeds))]
				// 时间单调不减，少量步进跨越鲜期/陈旧窗
				now += int64(rng.Intn(12))
				if rng.Intn(20) == 0 {
					now += 20
				}
				want := m.check(tok, need, now)
				got, gerr := g.Check(tok, need, now)
				log = append(log, fmt.Sprintf("input:  Check(%s,%v,%d)", tok, need, now))
				if errCmp(want.err, gerr) {
					failf("step %d Check(%s,%v,%d) err real=%v model=%v", step, tok, need, now, gerr, want.err)
				}
				if want.err == nil {
					gsrc := "None"
					if got.HasSrc {
						gsrc = srcName(got.Source)
					}
					log[len(log)-1] += fmt.Sprintf(" | output: %s/%s%s | basis: %s",
						verdictName(got.Verdict), gsrc, got.MissingInfo(), got.Reason)
					if got.Verdict != want.verdict || gsrc != want.src || got.Missing != want.missing {
						failf("step %d Check(%s,%v,%d) real=(%s/%s miss=%q) model=(%s/%s miss=%q)",
							step, tok, need, now, verdictName(got.Verdict), gsrc, got.Missing,
							verdictName(want.verdict), want.src, want.missing)
					}
				} else {
					log[len(log)-1] += fmt.Sprintf(" | rejected: %v", gerr)
				}
			default: // 20% Revoke
				sub := simSubs[rng.Intn(len(simSubs))]
				rt := int64(rng.Intn(10))
				if rng.Intn(20) == 0 {
					now += int64(rng.Intn(5))
				}
				wantErr := m.revoke(sub, rt, now)
				gotErr := g.Revoke(sub, rt, now)
				log = append(log, fmt.Sprintf("input:  Revoke(%s,t=%d,now=%d) -> real=%v model=%v", sub, rt, now, gotErr, wantErr))
				if errCmp(wantErr, gotErr) {
					failf("Revoke(%s,%d,%d) real=%v model=%v", sub, rt, now, gotErr, wantErr)
				}
			}
			// 每步比对 Calls
			if g.Calls() != m.calls {
				failf("step %d calls real=%d model=%d", step, g.Calls(), m.calls)
			}
		}
	}
}

func errCmp(a, b error) bool {
	return (a == nil) != (b == nil)
}
