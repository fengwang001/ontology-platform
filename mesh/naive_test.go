package mesh_test

import (
	"strings"

	"ontology/mesh"
)

// This file is an INDEPENDENT naive model written directly from the spec:
// rules are scanned linearly in declaration order and validation follows
// the documented phases. The real implementation shares no code with it.

func naiveStripQuery(p string) string {
	if i := strings.IndexByte(p, '?'); i >= 0 {
		return p[:i]
	}
	return p
}

func naiveSegments(p string) []string {
	s := strings.Split(p, "/")
	if len(s) > 1 && s[len(s)-1] == "" {
		s = s[:len(s)-1]
	}
	return s
}

func naiveSegmentPrefix(want, got string) bool {
	w, g := naiveSegments(want), naiveSegments(got)
	if len(w) > len(g) {
		return false
	}
	for i := range w {
		if w[i] != g[i] {
			return false
		}
	}
	return true
}

func naiveHeaderVal(op mesh.HeaderOp, want, got string) bool {
	switch op {
	case mesh.HeaderExact:
		return got == want
	case mesh.HeaderPrefix:
		return strings.HasPrefix(got, want)
	case mesh.HeaderExists:
		return true
	}
	return false
}

func naiveMatcher(m mesh.Matcher, path string, headers map[string][]string) bool {
	if m.Path != nil {
		switch m.Path.Kind {
		case mesh.PathExact:
			if m.Path.Value != path {
				return false
			}
		case mesh.PathPrefix:
			if !naiveSegmentPrefix(m.Path.Value, path) {
				return false
			}
		}
	}
	for _, hc := range m.Headers {
		vals, ok := headers[strings.ToLower(hc.Name)]
		if !ok {
			return false
		}
		sat := false
		for _, v := range vals {
			if naiveHeaderVal(hc.Op, hc.Value, v) {
				sat = true
				break
			}
		}
		if !sat {
			return false
		}
	}
	return true
}

// naiveRoute mirrors the spec: first rule whose ANY matcher matches wins;
// then fallback; target by consecutive bucket ownership; endpoint lookup.
func naiveRoute(s *mesh.Service, cfg *mesh.Config, ready map[string]bool, req mesh.Request) (subset string, rule int, target int, fallback bool, kind mesh.Kind) {
	path := naiveStripQuery(req.Path)
	headers := map[string][]string{}
	for n, vs := range req.Headers {
		headers[strings.ToLower(n)] = vs
	}
	var targets []mesh.Target
	rule = -1
	winner := -1
ruleLoop:
	for ri := range cfg.Rules {
		for _, m := range cfg.Rules[ri].Matchers {
			if naiveMatcher(m, path, headers) {
				winner = ri
				break ruleLoop
			}
		}
	}
	if winner >= 0 {
		targets = cfg.Rules[winner].Targets
		rule = winner
	} else if len(cfg.Fallback) > 0 {
		targets = cfg.Fallback
		fallback = true
	} else {
		return "", -1, -1, false, mesh.KindNoRoute
	}
	cut := 0
	target = -1
	for i, t := range targets {
		if req.Bucket < cut+t.Weight*100 {
			target = i
			subset = t.Subset
			break
		}
		cut += t.Weight * 100
	}
	if target < 0 || !ready[subset] {
		return "", rule, target, fallback, mesh.KindNoEndpoint
	}
	return subset, rule, target, fallback, -1
}

func naiveHeaderEntailed(a, b mesh.HeaderCondition) bool {
	if !strings.EqualFold(a.Name, b.Name) {
		return false
	}
	switch a.Op {
	case mesh.HeaderExists:
		return b.Op == mesh.HeaderExists
	case mesh.HeaderExact:
		switch b.Op {
		case mesh.HeaderExists:
			return true
		case mesh.HeaderPrefix:
			return strings.HasPrefix(a.Value, b.Value)
		case mesh.HeaderExact:
			return a.Value == b.Value
		}
	case mesh.HeaderPrefix:
		switch b.Op {
		case mesh.HeaderExists:
			return true
		case mesh.HeaderPrefix:
			return strings.HasPrefix(a.Value, b.Value)
		}
	}
	return false
}

func naivePathCovers(a, b *mesh.PathCondition) bool {
	if a == nil {
		return true
	}
	if b == nil {
		return false
	}
	if a.Kind == mesh.PathExact {
		return b.Kind == mesh.PathExact && a.Value == b.Value
	}
	return (b.Kind == mesh.PathExact || b.Kind == mesh.PathPrefix) &&
		naiveSegmentPrefix(a.Value, b.Value)
}

func naiveMatcherCovers(a, b mesh.Matcher) bool {
	if !naivePathCovers(a.Path, b.Path) {
		return false
	}
	for _, ah := range a.Headers {
		ok := false
		for _, bh := range b.Headers {
			if naiveHeaderEntailed(ah, bh) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// naivePublishError re-runs the four validation phases independently.
func naivePublishError(cfg *mesh.Config) (mesh.Kind, int) {
	if cfg == nil {
		return mesh.KindInvalidArgument, -1
	}
	checkShape := func(ri int, p *mesh.Policy) (mesh.Kind, int) {
		if p != nil {
			if (p.Timeout != nil && *p.Timeout < 0) ||
				(p.Retries != nil && *p.Retries < 0) ||
				(p.PerAttemptTime != nil && *p.PerAttemptTime < 0) {
				return mesh.KindInvalidArgument, ri
			}
		}
		return -1, 0
	}
	if k, ri := checkShape(-1, &cfg.Default); k >= 0 {
		return k, ri
	}
	for ri := range cfg.Rules {
		r := &cfg.Rules[ri]
		if len(r.Matchers) == 0 || len(r.Targets) == 0 {
			return mesh.KindInvalidArgument, ri
		}
		for _, m := range r.Matchers {
			if m.Path != nil && (m.Path.Kind != mesh.PathExact && m.Path.Kind != mesh.PathPrefix ||
				len(m.Path.Value) == 0 || m.Path.Value[0] != '/') {
				return mesh.KindInvalidArgument, ri
			}
			for _, h := range m.Headers {
				if h.Name == "" || h.Op < mesh.HeaderExact || h.Op > mesh.HeaderExists {
					return mesh.KindInvalidArgument, ri
				}
			}
		}
		if k, i := checkShape(ri, r.Policy); k >= 0 {
			return k, i
		}
	}
	checkWeights := func(ri int, ts []mesh.Target) (mesh.Kind, int) {
		if len(ts) == 0 {
			return -1, 0
		}
		sum := 0
		for _, t := range ts {
			if t.Subset == "" || t.Weight < 0 || t.Weight > 100 {
				return mesh.KindWeightInvalid, ri
			}
			sum += t.Weight
		}
		if sum != 100 {
			return mesh.KindWeightInvalid, ri
		}
		return -1, 0
	}
	for ri := range cfg.Rules {
		if k, i := checkWeights(ri, cfg.Rules[ri].Targets); k >= 0 {
			return k, i
		}
	}
	if k, i := checkWeights(-1, cfg.Fallback); k >= 0 {
		return k, i
	}
	eff := func(def mesh.Policy, ov *mesh.Policy) (t, r, pat *int) {
		t, r, pat = def.Timeout, def.Retries, def.PerAttemptTime
		if ov != nil {
			if ov.Timeout != nil {
				t = ov.Timeout
			}
			if ov.Retries != nil {
				r = ov.Retries
			}
			if ov.PerAttemptTime != nil {
				pat = ov.PerAttemptTime
			}
		}
		return
	}
	checkPolicy := func(ri int, p mesh.EffectivePolicy) (mesh.Kind, int) {
		if p.PerAttemptTime != nil && p.Timeout != nil && *p.PerAttemptTime > *p.Timeout {
			return mesh.KindPolicyInvalid, ri
		}
		if p.Retries != nil && p.PerAttemptTime != nil && p.Timeout != nil &&
			*p.Retries**p.PerAttemptTime > *p.Timeout {
			return mesh.KindPolicyInvalid, ri
		}
		return -1, 0
	}
	for ri := range cfg.Rules {
		t, r, pat := eff(cfg.Default, cfg.Rules[ri].Policy)
		if k, i := checkPolicy(ri, mesh.EffectivePolicy{Timeout: t, Retries: r, PerAttemptTime: pat}); k >= 0 {
			return k, i
		}
	}
	if t, r, pat := eff(cfg.Default, nil); true {
		if k, i := checkPolicy(-1, mesh.EffectivePolicy{Timeout: t, Retries: r, PerAttemptTime: pat}); k >= 0 {
			return k, i
		}
	}
	for i := 1; i < len(cfg.Rules); i++ {
		all := true
		for _, bm := range cfg.Rules[i].Matchers {
			cov := false
			for j := 0; j < i; j++ {
				for _, am := range cfg.Rules[j].Matchers {
					if naiveMatcherCovers(am, bm) {
						cov = true
						break
					}
				}
				if cov {
					break
				}
			}
			if !cov {
				all = false
				break
			}
		}
		if all {
			return mesh.KindShadowedRule, i
		}
	}
	return -1, -1
}
