package servicemesh

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

// diffLogger 与 opLogger 相同接口，供差分测试记录逐步对照过程。

var diffPathSegs = []string{"api", "v1", "v2", "users", "orders", "admin", "health", "x"}
var diffHeaderNames = []string{"x-env", "x-tenant", "x-debug", "x-region"}
var diffHeaderValues = []string{"prod", "dev", "team-a", "team-b", "cn", "us", "1", "product"}

type diffGen struct {
	rng *rand.Rand
}

func (g *diffGen) path(exactOnly bool) PathMatch {
	n := g.rng.Intn(3) // 0..2 段
	segs := make([]string, n)
	for i := range segs {
		segs[i] = diffPathSegs[g.rng.Intn(len(diffPathSegs))]
	}
	p := "/"
	for i, s := range segs {
		if i > 0 {
			p += "/"
		}
		p += s
	}
	kind := PathPrefix
	if !exactOnly && g.rng.Intn(2) == 0 {
		kind = PathExact
	}
	if n == 0 {
		kind = PathExact
	}
	return PathMatch{Kind: kind, Path: p}
}

func (g *diffGen) headers() []HeaderMatch {
	n := g.rng.Intn(3)
	used := map[string]bool{}
	out := []HeaderMatch{}
	for i := 0; i < n; i++ {
		name := diffHeaderNames[g.rng.Intn(len(diffHeaderNames))]
		if used[name] {
			continue
		}
		used[name] = true
		op := HeaderOp(g.rng.Intn(3))
		val := diffHeaderValues[g.rng.Intn(len(diffHeaderValues))]
		if op == HeaderPrefix && g.rng.Intn(2) == 0 {
			val = val[:g.rng.Intn(len(val))+1] // 随机更短前缀，可能是空串前缀
		}
		out = append(out, HeaderMatch{Name: name, Op: op, Value: val})
	}
	return out
}

// weights 生成和为 100 的合法目标权重。
func (g *diffGen) targets(pool []string) []Target {
	n := 1 + g.rng.Intn(3)
	names := g.rng.Perm(len(pool))[:n]
	weights := make([]int, n)
	remaining := 100
	for i := 0; i < n-1; i++ {
		weights[i] = g.rng.Intn(remaining + 1)
		remaining -= weights[i]
	}
	weights[n-1] = remaining
	targets := make([]Target, n)
	for i := range targets {
		targets[i] = Target{Subset: pool[names[i]], Weight: weights[i]}
	}
	return targets
}

func (g *diffGen) config(subsetPool []string) *ServiceConfig {
	nRules := 1 + g.rng.Intn(8)
	cfg := &ServiceConfig{}
	if g.rng.Intn(2) == 0 {
		toD := time.Duration(10+g.rng.Intn(200)) * time.Millisecond
		to := dur(toD)
		cfg.Default.Timeout = to
		if g.rng.Intn(2) == 0 {
			pa := dur(time.Duration(1+g.rng.Intn(int(toD/time.Millisecond))) * time.Millisecond)
			cfg.Default.PerAttemptTimeout = pa
		}
		if g.rng.Intn(2) == 0 {
			cfg.Default.MaxRetries = intPtr(g.rng.Intn(4))
		}
	}
	for i := 0; i < nRules; i++ {
		nMatches := 1 + g.rng.Intn(3)
		items := make([]MatchItem, nMatches)
		for j := range items {
			items[j] = MatchItem{Path: g.path(false), Headers: g.headers()}
		}
		r := Rule{Matches: items, Targets: g.targets(subsetPool)}
		if g.rng.Intn(2) == 0 {
			var ov Policy
			if g.rng.Intn(2) == 0 {
				toD := time.Duration(10+g.rng.Intn(200)) * time.Millisecond
				to := dur(toD)
				ov.Timeout = to
			}
			if g.rng.Intn(2) == 0 {
				pa := dur(time.Duration(1+g.rng.Intn(50)) * time.Millisecond)
				ov.PerAttemptTimeout = pa
			}
			if g.rng.Intn(2) == 0 {
				ov.MaxRetries = intPtr(g.rng.Intn(4))
			}
			r.Override = &ov
		}
		cfg.Rules = append(cfg.Rules, r)
	}
	if g.rng.Intn(2) == 0 {
		cfg.Fallbacks = g.targets(subsetPool)
	}
	return cfg
}

func (g *diffGen) request() Request {
	pm := g.path(true)
	// 偶尔制造半个段/多一段，以压测段边界。
	path := pm.Path
	switch g.rng.Intn(5) {
	case 0:
		path += "extra" // 半个段
	case 1:
		path += "/deep/seg"
	}
	if g.rng.Intn(2) == 0 {
		path += "?q=" + diffHeaderValues[g.rng.Intn(len(diffHeaderValues))]
	}
	hv := map[string][]string{}
	n := g.rng.Intn(4)
	for i := 0; i < n; i++ {
		name := diffHeaderNames[g.rng.Intn(len(diffHeaderNames))]
		vals := []string{diffHeaderValues[g.rng.Intn(len(diffHeaderValues))]}
		if g.rng.Intn(3) == 0 {
			vals = append(vals, diffHeaderValues[g.rng.Intn(len(diffHeaderValues))])
		}
		hv[name] = vals
	}
	return Request{Path: path, HeaderValues: hv, Bucket: g.rng.Intn(10000)}
}

// policyEqual 逐字段比较生效策略。
func policyEqual(a, b Policy) bool {
	eq := func(x, y *time.Duration) bool {
		if x == nil || y == nil {
			return x == nil && y == nil
		}
		return *x == *y
	}
	eqI := func(x, y *int) bool {
		if x == nil || y == nil {
			return x == nil && y == nil
		}
		return *x == *y
	}
	return eq(a.Timeout, b.Timeout) && eq(a.PerAttemptTimeout, b.PerAttemptTimeout) &&
		eqI(a.MaxRetries, b.MaxRetries)
}

func TestDifferentialAgainstNaiveModel(t *testing.T) {
	l := newLogger(t)
	const iterations = 400
	const requestsPerCfg = 120
	differentialSeed(t, l, 20261008, iterations, requestsPerCfg)
}

// differentialSeeds 用多个独立种子重放，提高随机覆盖面。
func differentialSeeds(t *testing.T) {
	l := newLogger(t)
	for _, seed := range []int64{1, 42, 777, 31337, 999983} {
		differentialSeed(t, l, seed, 120, 80)
	}
}

func differentialSeed(t *testing.T, l *opLogger, seed int64, iterations, requestsPerCfg int) {
	rng := rand.New(rand.NewSource(seed))
	pool := []string{"s0", "s1", "s2", "s3", "s4"}

	totalReqs := 0
	for iter := 0; iter < iterations; iter++ {
		g := &diffGen{rng: rand.New(rand.NewSource(rng.Int63()))}
		cfg := g.config(pool)

		// 先对照遮蔽判定：朴素模型与实现应一致。
		_, naiveShadow := naiveShadowRejected(cfg)
		m := NewMesh()
		_, pubErr := m.Publish("svc", cfg, 0)
		realReject := pubErr != nil
		// 权重由生成器保证合法；策略可能随机非法，朴素模型只负责遮蔽，
		// 故遮蔽对照仅在策略不非法时进行。
		if !realReject || (pubErr.Class == ClassValidation && pubErr.Kind == ValShadowed) {
			if realReject != naiveShadow {
				t.Fatalf("iter=%d 遮蔽判定分歧: real=%v naive=%v", iter, realReject, naiveShadow)
			}
		}
		if realReject {
			l.publish("svc", cfg, 0, 0, pubErr, pubErr.Class)
			l.log("iter=%d 朴素遮蔽=%v，发布被拒（随机策略也可能非法），跳过请求对照",
				iter, naiveShadow)
			continue
		}
		l.publish("svc", cfg, 0, 1, nil, -1)

		// 随机登记子集：部分子集缺失或无就绪端点。
		registered := map[string]bool{}
		subsets := map[string][]Endpoint{}
		for _, name := range pool {
			if rng.Intn(4) == 0 {
				continue // 未登记
			}
			ready := rng.Intn(4) != 0
			subsets[name] = []Endpoint{{Name: name + "-ep", Ready: ready}}
			registered[name] = ready
		}
		if e := m.RegisterSubsets("svc", subsets); e != nil {
			t.Fatal(e)
		}

		for rq := 0; rq < requestsPerCfg; rq++ {
			req := g.request()
			totalReqs++
			res, rerr := m.Route("svc", req)
			nr := naiveRoute(cfg, registered, req.Path, normalizeHeaders(req), req.Bucket)

			// 对照错误类别与选取结果。
			var realClass ErrClass = -1
			if rerr != nil {
				realClass = rerr.Class
			}
			var basis string
			switch {
			case nr.ruleIdx == -2:
				basis = fmt.Sprintf("朴素: 无路由；real class=%d", realClass)
				if realClass != ClassNoRoute {
					t.Fatalf("iter=%d req=%+v %s", iter, req, basis)
				}
			case nr.noEp:
				basis = fmt.Sprintf("朴素: subset=%q 无可用端点；real class=%d", nr.subset, realClass)
				if realClass != ClassNoEndpoint {
					t.Fatalf("iter=%d req=%+v %s", iter, req, basis)
				}
			default:
				if rerr != nil {
					t.Fatalf("iter=%d 意外错误 %v (朴素=%s)", iter, rerr, nr)
				}
				if res.RuleIdx != nr.ruleIdx || res.Subset != nr.subset {
					t.Fatalf("iter=%d 路由分歧: real(rule=%d subset=%s) naive%s req=%+v",
						iter, res.RuleIdx, res.Subset, nr, req)
				}
				if !policyEqual(res.Policy, nr.policy) {
					t.Fatalf("iter=%d 策略分歧: real=%+v naive=%+v", iter, res.Policy, nr.policy)
				}
				basis = fmt.Sprintf("朴素=%s 实际 rule=%d subset=%q 策略一致", nr, res.RuleIdx, res.Subset)
			}
			// 只详细打印前若干步，避免日志过长；其余按批汇总。
			if iter < 3 && rq < 8 {
				l.route("svc", req, res, rerr, basis)
			}
		}
		l.log("iter=%d 完成 %d 个随机请求与朴素模型逐步对照，全部一致", iter, requestsPerCfg)
	}
	l.log("差分测试(seed=%d)结束：共对照 %d 个随机请求", seed, totalReqs)
}
