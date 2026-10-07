package exports

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// naiveResolve 是独立的朴素参照模型：逐键扫描选择、递归解析目标。
// 它不复用被测实现的键选择索引，用于差分对照。
func naiveResolve(entries []Entry, subpath string, conditions []string) (Resolution, error) {
	condSet, err := validateRequest(subpath, conditions)
	if err != nil {
		return Resolution{}, err
	}
	key, target, matched, ok := naiveSelectKey(entries, subpath)
	if !ok {
		return Resolution{}, errf(KindSubpathNotExported, "子路径 %q 未导出", subpath)
	}
	hasStar := strings.IndexByte(key, '*') >= 0
	final, seq, rerr := naiveResolveTarget(target, condSet, matched, hasStar)
	if rerr != nil {
		return Resolution{}, rerr
	}
	return Resolution{Target: final, Key: key, Matched: matched, Conditions: seq}, nil
}

// naiveSelectKey 逐键扫描：精确键优先，否则按规则挑选最优通配键。
func naiveSelectKey(entries []Entry, request string) (key string, target Target, matched string, ok bool) {
	bestPrefixLen, bestKeyLen := -1, -1
	for _, e := range entries {
		if e.Key == request {
			return e.Key, e.Target, "", true
		}
		star := strings.IndexByte(e.Key, '*')
		if star < 0 {
			continue
		}
		prefix, suffix := e.Key[:star], e.Key[star+1:]
		if !strings.HasPrefix(request, prefix) || !strings.HasSuffix(request, suffix) {
			continue
		}
		seg := len(request) - len(prefix) - len(suffix)
		if seg < 1 {
			continue
		}
		if len(prefix) > bestPrefixLen ||
			(len(prefix) == bestPrefixLen && len(e.Key) > bestKeyLen) {
			bestPrefixLen, bestKeyLen = len(prefix), len(e.Key)
			key, target = e.Key, e.Target
			matched = request[len(prefix) : len(request)-len(suffix)]
			ok = true
		}
	}
	return key, target, matched, ok
}

// naiveResolveTarget 递归解析目标，语义与规格逐条对应。
func naiveResolveTarget(t Target, condSet map[string]struct{}, matched string, hasStar bool) (string, []string, *Error) {
	switch t.kind {
	case targetString:
		s := t.value
		if hasStar {
			s = strings.ReplaceAll(s, "*", matched)
		}
		if err := validateTargetString(s); err != nil {
			return "", nil, err
		}
		return s, nil, nil
	case targetForbidden:
		return "", nil, errf(KindForbidden, "目标被显式禁止")
	default:
		for _, pair := range t.conds {
			if pair.Condition != "default" {
				if _, active := condSet[pair.Condition]; !active {
					continue
				}
			}
			s, seq, err := naiveResolveTarget(pair.Target, condSet, matched, hasStar)
			if err == nil {
				return s, append([]string{pair.Condition}, seq...), nil
			}
			if err.Kind == KindNoMatchingCondition {
				continue
			}
			return "", nil, err
		}
		return "", nil, errf(KindNoMatchingCondition, "条件映射中没有条件命中")
	}
}

// ---- 随机表与请求生成 ----

var (
	randKeyPrefixes = []string{"./", "./", "./a/", "./a/", "./a/b/", "./b", "./b/", "./node_modules/"}
	randKeySuffixes = []string{"", "", "", ".js", "/x", ".json"}
	randSegments    = []string{"a", "b", "x", "a/b", "..", ".", "node_modules", "NODE_MODULES", "c.js"}
	randCondNames   = []string{"node", "import", "require", "browser", "x", "y"}
	randTargets     = []string{
		"./ok.js", "./dist/*", "./out/*/x.js", "./a/b",
		"./bad/../x", "./bad/./x", "./x/node_modules/y", "not-relative", "./star/*/*",
	}
)

func randKey(rng *rand.Rand) string {
	prefix := randKeyPrefixes[rng.Intn(len(randKeyPrefixes))]
	suffix := randKeySuffixes[rng.Intn(len(randKeySuffixes))]
	if rng.Intn(2) == 0 {
		// 精确键
		mid := randSegments[rng.Intn(len(randSegments))]
		if strings.ContainsAny(mid, "*") {
			mid = "a"
		}
		return prefix + mid + suffix
	}
	return prefix + "*" + suffix
}

func randTarget(rng *rand.Rand, depth int) Target {
	roll := rng.Intn(10)
	switch {
	case roll < 5 || depth >= 2:
		return StringTarget(randTargets[rng.Intn(len(randTargets))])
	case roll < 6:
		return ForbiddenTarget()
	default:
		n := 1 + rng.Intn(3)
		pairs := make([]CondPair, 0, n)
		used := map[string]bool{}
		for i := 0; i < n; i++ {
			var name string
			if i == n-1 && rng.Intn(2) == 0 {
				name = "default"
			} else {
				name = randCondNames[rng.Intn(len(randCondNames))]
				for used[name] {
					name = randCondNames[rng.Intn(len(randCondNames))]
				}
			}
			if used[name] {
				continue
			}
			used[name] = true
			pairs = append(pairs, Cond(name, randTarget(rng, depth+1)))
		}
		if len(pairs) == 0 {
			return StringTarget("./ok.js")
		}
		return ConditionsTarget(pairs...)
	}
}

func randTable(rng *rand.Rand) []Entry {
	n := 1 + rng.Intn(12)
	seen := map[string]bool{}
	entries := make([]Entry, 0, n)
	for i := 0; i < n; i++ {
		key := randKey(rng)
		if seen[key] {
			continue
		}
		seen[key] = true
		entries = append(entries, Entry{Key: key, Target: randTarget(rng, 0)})
	}
	if rng.Intn(4) == 0 {
		entries = append(entries, Entry{Key: ".", Target: randTarget(rng, 0)})
	}
	return entries
}

func randRequest(rng *rand.Rand) (string, []string) {
	var subpath string
	switch rng.Intn(10) {
	case 0:
		subpath = "."
	case 1:
		subpath = "bad-subpath" // 非法请求
	case 2:
		subpath = "./a*" // 非法请求：含星号
	default:
		prefix := randKeyPrefixes[rng.Intn(len(randKeyPrefixes))]
		subpath = prefix + randSegments[rng.Intn(len(randSegments))]
		if rng.Intn(3) == 0 {
			subpath += randKeySuffixes[rng.Intn(len(randKeySuffixes))]
		}
	}
	var conds []string
	for i := 0; i < rng.Intn(4); i++ {
		conds = append(conds, randCondNames[rng.Intn(len(randCondNames))])
	}
	return subpath, conds
}

func summarize(entries []Entry) string {
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "  %q => %s\n", e.Key, describeTarget(e.Target))
	}
	return b.String()
}

func describeTarget(t Target) string {
	switch t.kind {
	case targetString:
		return fmt.Sprintf("%q", t.value)
	case targetForbidden:
		return "null"
	default:
		parts := make([]string, 0, len(t.conds))
		for _, p := range t.conds {
			parts = append(parts, fmt.Sprintf("%q: %s", p.Condition, describeTarget(p.Target)))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
}

func outcomeString(res Resolution, err error) string {
	if err != nil {
		if kind, ok := KindOf(err); ok {
			return "error(" + kind.String() + ")"
		}
		return "error(?)"
	}
	return fmt.Sprintf("ok(target=%q key=%q matched=%q conds=%v)",
		res.Target, res.Key, res.Matched, res.Conditions)
}

// TestDifferentialAgainstNaiveModel 用大量随机表与请求，
// 对照被测实现与朴素模型的结果，并在日志中打印每次输入、输出与判定依据。
func TestDifferentialAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	const tables = 400
	const requestsPerTable = 25
	for i := 0; i < tables; i++ {
		entries := randTable(rng)
		r, err := NewResolver(entries)
		if err != nil {
			t.Fatalf("随机表应合法: %v\n%s", err, summarize(entries))
		}
		for j := 0; j < requestsPerTable; j++ {
			subpath, conds := randRequest(rng)
			gotRes, gotErr := r.Resolve(subpath, conds)
			wantRes, wantErr := naiveResolve(entries, subpath, conds)

			gotKind, gotIsErr := KindOf(gotErr)
			wantKind, wantIsErr := KindOf(wantErr)
			match := gotIsErr == wantIsErr
			if match {
				if gotIsErr {
					match = gotKind == wantKind
				} else {
					match = reflect.DeepEqual(gotRes, wantRes)
				}
			}
			t.Logf("表#%d 请求#%d 输入 subpath=%q conds=%v\n表:\n%s被测: %s\n朴素: %s\n一致: %v",
				i, j, subpath, conds, summarize(entries),
				outcomeString(gotRes, gotErr), outcomeString(wantRes, wantErr), match)
			if !match {
				t.Fatalf("差分不一致: subpath=%q conds=%v\n表:\n%s被测: %s\n朴素: %s",
					subpath, conds, summarize(entries),
					outcomeString(gotRes, gotErr), outcomeString(wantRes, wantErr))
			}
		}
	}
}
