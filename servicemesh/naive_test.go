package servicemesh

// naive_model_test.go 提供与实现完全独立的朴素参考模型：
// 逐条规则、逐个匹配项线性扫描。随机差分测试用它对照索引实现。

import (
	"fmt"
	"strings"
)

type naiveResult struct {
	ruleIdx int // -1 兜底，-2 无路由
	subset  string
	noEp    bool
	policy  Policy
}

func naiveStripQuery(p string) string {
	if i := strings.IndexByte(p, '?'); i >= 0 {
		return p[:i]
	}
	return p
}

func naivePathMatch(kind PathKind, cond, path string) bool {
	if kind == PathExact {
		return path == cond
	}
	if path == cond {
		return true
	}
	return strings.HasPrefix(path, cond) && strings.HasPrefix(path[len(cond):], "/")
}

func naiveHeaderMatch(h HeaderMatch, vals []string) bool {
	for _, v := range vals {
		switch h.Op {
		case HeaderPresent:
			return true
		case HeaderExact:
			if v == h.Value {
				return true
			}
		case HeaderPrefix:
			if strings.HasPrefix(v, h.Value) {
				return true
			}
		}
	}
	return false
}

func naiveItemMatches(item MatchItem, path string, headers map[string][]string) bool {
	if !naivePathMatch(item.Path.Kind, item.Path.Path, path) {
		return false
	}
	for _, h := range item.Headers {
		if !naiveHeaderMatch(h, headers[strings.ToLower(strings.TrimSpace(h.Name))]) {
			return false
		}
	}
	return true
}

// naiveRoute 完全按规格线性求值（子集存在性由 subsets 提供）。
func naiveRoute(cfg *ServiceConfig, subsets map[string]bool, path string,
	headers map[string][]string, bucket int) naiveResult {
	path = naiveStripQuery(path)
	ruleIdx := -1
	var targets []Target
	var eff Policy
	for ri, r := range cfg.Rules {
		hit := false
		for _, item := range r.Matches {
			if naiveItemMatches(item, path, headers) {
				hit = true
				break
			}
		}
		if hit {
			ruleIdx = ri
			targets = r.Targets
			if r.Override != nil {
				eff = naiveMerge(*r.Override, cfg.Default)
			} else {
				eff = cfg.Default
			}
			break
		}
	}
	if ruleIdx == -1 {
		if len(cfg.Fallbacks) == 0 {
			return naiveResult{ruleIdx: -2}
		}
		targets = cfg.Fallbacks
		eff = cfg.Default
	}
	start := 0
	subset := ""
	for _, t := range targets {
		end := start + t.Weight*100
		if bucket >= start && bucket < end {
			subset = t.Subset
			break
		}
		start = end
	}
	res := naiveResult{ruleIdx: ruleIdx, subset: subset, policy: eff}
	if subset == "" || !subsets[subset] {
		res.noEp = true
	}
	return res
}

func naiveMerge(over, def Policy) Policy {
	out := def
	if over.Timeout != nil {
		v := *over.Timeout
		out.Timeout = &v
	}
	if over.PerAttemptTimeout != nil {
		v := *over.PerAttemptTimeout
		out.PerAttemptTimeout = &v
	}
	if over.MaxRetries != nil {
		v := *over.MaxRetries
		out.MaxRetries = &v
	}
	return out
}

// naivePublishable 朴素地判断配置是否应被拒绝为遮蔽。
func naiveShadowRejected(cfg *ServiceConfig) (int, bool) {
	for ri := 1; ri < len(cfg.Rules); ri++ {
		all := true
		for _, cur := range cfg.Rules[ri].Matches {
			covered := false
			for pj := 0; pj < ri; pj++ {
				for _, prev := range cfg.Rules[pj].Matches {
					if naiveCovers(prev, cur) {
						covered = true
					}
				}
			}
			if !covered {
				all = false
				break
			}
		}
		if all {
			return ri, true
		}
	}
	return -1, false
}

func naiveCovers(a, b MatchItem) bool {
	if !naivePathCovers(a.Path, b.Path) {
		return false
	}
	for _, ah := range a.Headers {
		ok := false
		for _, bh := range b.Headers {
			if strings.EqualFold(strings.TrimSpace(ah.Name), strings.TrimSpace(bh.Name)) &&
				naiveEntails(ah, bh) {
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

func naivePathCovers(a, b PathMatch) bool {
	if a.Kind == PathExact {
		return b.Kind == PathExact && a.Path == b.Path
	}
	return naivePathMatch(PathPrefix, a.Path, b.Path)
}

func naiveEntails(a, b HeaderMatch) bool {
	switch a.Op {
	case HeaderExact:
		switch b.Op {
		case HeaderExact:
			return a.Value == b.Value
		case HeaderPrefix:
			return strings.HasPrefix(a.Value, b.Value)
		default:
			return true
		}
	case HeaderPrefix:
		if b.Op == HeaderPresent {
			return true
		}
		return b.Op == HeaderPrefix && strings.HasPrefix(a.Value, b.Value)
	default:
		return b.Op == HeaderPresent
	}
}

func (r naiveResult) String() string {
	return fmt.Sprintf("{rule=%d subset=%q noEp=%v}", r.ruleIdx, r.subset, r.noEp)
}
