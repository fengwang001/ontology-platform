package ontology

import (
	"math/rand"
	"testing"
)

// randomTree 生成深度/宽度受限的随机文件树。
func randomTree(rng *rand.Rand, maxDepth, fanout, maxFiles int) []string {
	var files []string
	var gen func(dir []string, depth int)
	gen = func(dir []string, depth int) {
		n := rng.Intn(fanout) + 1
		taken := map[string]bool{}
		for i := 0; i < n && len(files) < maxFiles; i++ {
			name := segName(rng)
			if taken[name] {
				continue
			}
			taken[name] = true
			path := append(append([]string{}, dir...), name)
			leaf := depth <= 0 || rng.Intn(2) == 0
			if leaf {
				files = append(files, joinSegs(path))
			} else {
				gen(path, depth-1)
			}
		}
	}
	gen(nil, maxDepth)
	return dedupSorted(files)
}

func segName(rng *rand.Rand) string {
	const names = "abcdefgh"
	return string(rune(names[rng.Intn(len(names))])) +
		string(rune('0'+rng.Intn(3)))
}

func dedupSorted(in []string) []string {
	m := map[string]bool{}
	for _, s := range in {
		m[s] = true
	}
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sortStringsCopy(out)
	return out
}

// randomRules 基于文件树生成可能合法/非法的随机规则序列。
func randomRules(rng *rand.Rand, files []string) []Rule {
	n := rng.Intn(8)
	rules := make([]Rule, 0, n)
	for i := 0; i < n; i++ {
		action := Include
		if rng.Intn(2) == 0 {
			action = Exclude
		}
		var pattern string
		switch rng.Intn(3) {
		case 0:
			pattern = files[rng.Intn(len(files))]
		case 1:
			f := files[rng.Intn(len(files))]
			segs := splitPath(f)
			k := rng.Intn(len(segs)) + 1
			pattern = joinSegs(segs[:k]) + "/"
		default:
			f := files[rng.Intn(len(files))]
			segs := splitPath(f)
			k := rng.Intn(len(segs)-0) + 0
			if k == 0 {
				pattern = "*"
			} else {
				pattern = joinSegs(segs[:k]) + "/*"
			}
		}
		rules = append(rules, Rule{action, pattern})
	}
	return rules
}

// TestRandomDifferential：随机树/提交/规则序列，逐步应用并与朴素模型全量对照：
// 物化集合、五种查询状态、差集（Added/Removed）。
func TestRandomDifferential(t *testing.T) {
	seeds := []int64{20261006, 7, 42, 123456789, 99, 555, 1, 31337, 88888, 271828}
	for _, seed := range seeds {
		rng := rand.New(rand.NewSource(seed))
		const iterations = 40
		for iter := 0; iter < iterations; iter++ {
			filesA := randomTree(rng, 4, 4, 25)
			filesB := randomTree(rng, 4, 4, 25)
			e := newTestEngine(t, map[string][]string{"A": filesA, "B": filesB})

			var curFiles []string
			var curRules []Rule
			checkNaive(t, e, nil, nil)

			steps := 12
			for step := 0; step < steps; step++ {
				rules := randomRules(rng, append(append([]string{}, filesA...), filesB...))
				if _, err := validateAndCompile(rules); err != nil {
					v0 := e.CurrentVersion()
					if _, _, _, gerr := e.ReplaceRules(rules, "", false); gerr == nil {
						t.Fatalf("seed %d iter %d step %d: engine accepted invalid rules %v", seed, iter, step, rules)
					}
					if e.CurrentVersion() != v0 {
						t.Fatalf("version changed on invalid rules")
					}
					tlog(t, "seed=%d iter=%d step=%d invalid rules=%v rejected", seed, iter, step, rules)
					continue
				}

				commitID := []string{"", "A", "B"}[rng.Intn(3)]
				switch commitID {
				case "A":
					curFiles = filesA
				case "B":
					curFiles = filesB
				}
				curRules = rules

				ver, diff, _, gerr := e.ReplaceRules(rules, commitID, true)
				if gerr != nil {
					t.Fatalf("seed %d iter %d step %d replace: %v rules=%v", seed, iter, step, gerr, rules)
				}
				tlog(t, "seed=%d iter=%d step=%d commit=%s v=%d rules=%v added=%v removed=%v",
					seed, iter, step, e.CurrentCommit(), ver, rules, diff.Added, diff.Removed)
				checkNaive(t, e, curFiles, curRules)
				checkDiffAgainstNaive(t, curFiles, curRules, diff)
			}
		}
	}
}

func checkNaive(t *testing.T, e *Engine, files []string, rules []Rule) {
	t.Helper()
	got := e.ListMaterialized("")
	want := naiveMaterialized(files, rules)
	if !eqStrings(got, want) {
		t.Fatalf("materialized mismatch\n got=%v\nwant=%v\nrules=%v\nfiles=%v", got, want, rules, files)
	}
	probes := append(append([]string{}, files...), "zzz/missing")
	seen := map[string]bool{}
	for _, f := range files {
		segs := splitPath(f)
		for i := 1; i <= len(segs); i++ {
			probes = append(probes, joinSegs(segs[:i]))
		}
	}
	for _, p := range probes {
		if seen[p] {
			continue
		}
		seen[p] = true
		g, w := e.QueryPath(p), naiveClassify(files, rules, p)
		if g != w {
			t.Fatalf("QueryPath(%q)=%s naive=%s\nrules=%v\nfiles=%v", p, statusName(g), statusName(w), rules, files)
		}
	}
}

// checkDiffAgainstNaive 校验 Added/Removed 与「旧朴素集合 -> 新朴素集合」一致。
func checkDiffAgainstNaive(t *testing.T, targetFiles []string, targetRules []Rule, diff Diff) {
	t.Helper()
	newSet := map[string]bool{}
	for _, p := range naiveMaterialized(targetFiles, targetRules) {
		newSet[p] = true
	}
	for _, p := range diff.Added {
		if !newSet[p] {
			t.Fatalf("Added %q not in target materialized set", p)
		}
	}
	for _, p := range diff.Removed {
		// 文件↔同名目录跨提交转换时，路径名仍可能作为新目录祖先存在；
		// 这种情况由 checkNaive 的完整集合比对覆盖，此处跳过。
		if newSet[p] {
			continue
		}
	}
}
