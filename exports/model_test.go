package exports

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// naiveResolve 是独立的朴素参照模型：逐键扫描、逐条件展开，
// 不使用任何索引结构，用于对照验证优化实现的正确性。
func naiveResolve(entries []Entry, subpath string, conditions []string) (Result, error) {
	if subpath != "." && !strings.HasPrefix(subpath, "./") {
		return Result{}, newError(KindInvalidRequest, "bad subpath %q", subpath)
	}
	if strings.Contains(subpath, "*") {
		return Result{}, newError(KindInvalidRequest, "star in subpath %q", subpath)
	}
	for _, c := range conditions {
		if c == "" {
			return Result{}, newError(KindInvalidRequest, "empty condition")
		}
	}
	condSet := map[string]bool{}
	for _, c := range conditions {
		condSet[c] = true
	}

	// 子路径选择：先精确，再逐键扫描通配。
	best := -1
	bestPrefix, bestKeyLen := -1, -1
	for i, e := range entries {
		if e.Key == subpath {
			best = i
			break
		}
		star := strings.IndexByte(e.Key, '*')
		if star < 0 {
			continue
		}
		prefix, suffix := e.Key[:star], e.Key[star+1:]
		if !strings.HasPrefix(subpath, prefix) || !strings.HasSuffix(subpath, suffix) {
			continue
		}
		if len(subpath)-len(prefix)-len(suffix) < 1 {
			continue
		}
		if len(prefix) > bestPrefix ||
			(len(prefix) == bestPrefix && len(e.Key) > bestKeyLen) {
			best, bestPrefix, bestKeyLen = i, len(prefix), len(e.Key)
		}
	}
	if best < 0 {
		return Result{}, newError(KindNotExported, "not exported: %q", subpath)
	}
	key := entries[best].Key
	matched, hasStar := "", false
	if star := strings.IndexByte(key, '*'); star >= 0 {
		hasStar = true
		matched = subpath[star : len(subpath)-(len(key)-star-1)]
	}
	var chain []string
	out, err := naiveTarget(entries[best].Target, condSet, matched, hasStar, &chain)
	if err != nil {
		return Result{}, err
	}
	return Result{Target: out, Key: key, Conditions: chain}, nil
}

func naiveTarget(tgt Target, conds map[string]bool, matched string, hasStar bool, chain *[]string) (string, error) {
	switch tgt.Kind() {
	case TargetForbidden:
		return "", newError(KindForbidden, "forbidden")
	case TargetString:
		s := tgt.String()
		if hasStar {
			s = strings.ReplaceAll(s, "*", matched)
		}
		if !strings.HasPrefix(s, "./") {
			return "", newError(KindInvalidTarget, "bad target %q", s)
		}
		for _, seg := range strings.Split(s[2:], "/") {
			if seg == "." || seg == ".." || strings.EqualFold(seg, "node_modules") {
				return "", newError(KindInvalidTarget, "bad segment in %q", s)
			}
		}
		return s, nil
	case TargetConditions:
		for _, c := range tgt.Conditions() {
			if c.Name != "default" && !conds[c.Name] {
				continue
			}
			mark := len(*chain)
			*chain = append(*chain, c.Name)
			out, err := naiveTarget(*c.Target, conds, matched, hasStar, chain)
			if err != nil {
				if IsKind(err, KindNoMatchingCondition) {
					*chain = (*chain)[:mark]
					continue
				}
				return "", err
			}
			return out, nil
		}
		return "", newError(KindNoMatchingCondition, "no condition matched")
	}
	return "", newError(KindInvalidTable, "unknown target")
}

var (
	randExactKeys = []string{".", "./a", "./b", "./a/b", "./utils", "./utils/format", "./x/y/z"}
	randWildKeys  = []string{"./*", "./a/*", "./*.js", "./x/*/y", "./features/*", "./a*b", "./pre*post", "./utils/*"}
	randStrTgts   = []string{"./ok.js", "./dist/*.js", "./x/*", "./deep/./bad.js", "./nm/node_modules/x.js", "no-prefix.js", "./a/../b.js", "./out/*"}
	randCondNames = []string{"a", "b", "browser", "node", "default"}
	randReqPaths  = []string{".", "./a", "./b", "./a/b", "./a/c", "./a/b/c", "./x/1/y", "./features/f1", "./features/node_modules/x", "./utils", "./utils/format", "./preZpost", "./aZb", "./unknown", "./", "./a.js", "./x/y/z"}
	randReqConds  = []string{"a", "b", "browser", "node", "Browser"}
)

func genTarget(r *rand.Rand, depth int) Target {
	roll := r.Intn(10)
	switch {
	case roll < 5 || depth >= 2:
		return StringTarget(randStrTgts[r.Intn(len(randStrTgts))])
	case roll < 6:
		return ForbiddenTarget()
	default:
		n := 1 + r.Intn(3)
		names := append([]string(nil), randCondNames...)
		r.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
		picked := append([]string(nil), names[:n]...)
		for i, nm := range picked {
			if nm == "default" && i != len(picked)-1 {
				picked = append(picked[:i], picked[i+1:]...)
				picked = append(picked, "default")
				break
			}
		}
		conds := make([]Condition, 0, len(picked))
		for _, nm := range picked {
			conds = append(conds, Cond(nm, genTarget(r, depth+1)))
		}
		return ConditionsTarget(conds...)
	}
}

func genEntries(r *rand.Rand) []Entry {
	n := 1 + r.Intn(8)
	keys := map[string]bool{}
	var entries []Entry
	for len(entries) < n {
		var key string
		if r.Intn(2) == 0 {
			key = randExactKeys[r.Intn(len(randExactKeys))]
		} else {
			key = randWildKeys[r.Intn(len(randWildKeys))]
		}
		if keys[key] {
			continue
		}
		keys[key] = true
		entries = append(entries, Entry{Key: key, Target: genTarget(r, 0)})
	}
	return entries
}

func genConds(r *rand.Rand) []string {
	var out []string
	for _, c := range randReqConds {
		if r.Intn(2) == 0 {
			out = append(out, c)
		}
	}
	return out
}

func describeEntries(entries []Entry) string {
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "  %q => %s\n", e.Key, describeTarget(e.Target, "    "))
	}
	return b.String()
}

func describeTarget(t Target, indent string) string {
	switch t.Kind() {
	case TargetString:
		return fmt.Sprintf("%q", t.String())
	case TargetForbidden:
		return "null"
	default:
		var b strings.Builder
		b.WriteString("{\n")
		for _, c := range t.Conditions() {
			fmt.Fprintf(&b, "%s%q: %s\n", indent, c.Name, describeTarget(*c.Target, indent+"  "))
		}
		return b.String() + indent[:len(indent)-2] + "}"
	}
}

func TestRandomizedAgainstNaiveModel(t *testing.T) {
	r := rand.New(rand.NewSource(20261006))
	const tables = 400
	const reqsPerTable = 8
	for i := 0; i < tables; i++ {
		entries := genEntries(r)
		tbl, err := NewTable(entries)
		if err != nil {
			t.Fatalf("generated table must be valid: %v", err)
		}
		for j := 0; j < reqsPerTable; j++ {
			subpath := randReqPaths[r.Intn(len(randReqPaths))]
			conds := genConds(r)

			gotRes, gotErr := tbl.Resolve(subpath, conds)
			wantRes, wantErr := naiveResolve(entries, subpath, conds)

			gotKind, wantKind := ErrorKind(-1), ErrorKind(-1)
			if gotErr != nil {
				gotKind = gotErr.(*Error).Kind
			}
			if wantErr != nil {
				wantKind = wantErr.(*Error).Kind
			}
			t.Logf("table#%d:\n%sreq=%q conds=%v => impl={%+v err=%v} model={%+v err=%v}",
				i, describeEntries(entries), subpath, conds, gotRes, gotErr, wantRes, wantErr)

			if gotKind != wantKind {
				t.Fatalf("table#%d req=%q conds=%v: error kind mismatch: impl=%v model=%v\nentries:\n%s",
					i, subpath, conds, gotErr, wantErr, describeEntries(entries))
			}
			if gotErr == nil && !reflect.DeepEqual(gotRes, wantRes) {
				t.Fatalf("table#%d req=%q conds=%v: result mismatch: impl=%+v model=%+v\nentries:\n%s",
					i, subpath, conds, gotRes, wantRes, describeEntries(entries))
			}
		}
	}
}
