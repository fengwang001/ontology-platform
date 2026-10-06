package sparse

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// This file differential-tests the engine against an independent naive
// model: the model materializes the full file set by scanning every rule
// linearly for every path, with no tries, hashes or incremental state.

func parentOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}

// patternHits mirrors the spec: exact hits itself; "dir/" hits all
// descendants; "dir/*" (or "*") hits direct children only.
func patternHits(pat, path string) bool {
	if strings.HasSuffix(pat, "/") {
		return strings.HasPrefix(path, pat) && len(path) > len(pat)
	}
	if pat == "*" {
		return !strings.Contains(path, "/")
	}
	if strings.HasSuffix(pat, "/*") {
		return parentOf(path) == pat[:len(pat)-2]
	}
	return pat == path
}

func naiveMatch(rules []Rule, path string) (include, matched bool) {
	for _, r := range rules {
		if patternHits(r.Pattern, path) {
			include = r.Action == Include
			matched = true
		}
	}
	return include, matched
}

// naiveMaterialized: files whose last hit is include, plus all ancestors.
func naiveMaterialized(files []string, rules []Rule) map[string]bool {
	mat := map[string]bool{}
	for _, f := range files {
		if inc, _ := naiveMatch(rules, f); inc {
			mat[f] = true
			for _, d := range ancestors(f) {
				mat[d] = true
			}
		}
	}
	return mat
}

func naiveQuery(files map[string]bool, rules []Rule, mat map[string]bool, path string) PathStatus {
	if files[path] {
		inc, matched := naiveMatch(rules, path)
		if inc {
			return StatusMaterialized
		}
		if matched {
			return StatusExcludedByRule
		}
		return StatusNoMatchingRule
	}
	isDir := false
	for f := range files {
		if strings.HasPrefix(f, path+"/") {
			isDir = true
			break
		}
	}
	if isDir {
		if mat[path] {
			return StatusMaterialized
		}
		inc, matched := naiveMatch(rules, path)
		if matched && !inc {
			return StatusExcludedByRule
		}
		if !matched {
			return StatusNoMatchingRule
		}
		return StatusEmptyDir
	}
	return StatusNotInCommit
}

// modelRulesetValid mirrors the documented validation rules.
func modelRulesetValid(rules []Rule) bool {
	seen := map[Rule]bool{}
	for _, r := range rules {
		p := r.Pattern
		if p == "" || strings.Contains(p, "//") {
			return false
		}
		body := p
		isPrefix := strings.HasSuffix(p, "/")
		if isPrefix {
			body = p[:len(p)-1]
		}
		for i, s := range strings.Split(body, "/") {
			if s == "" || s == ".." {
				return false
			}
			if s == "*" && !isPrefix && i != len(strings.Split(body, "/"))-1 {
				return false
			}
		}
		if seen[r] {
			return false
		}
		seen[r] = true
	}
	return true
}

type modelState struct {
	commits  map[string]map[string]bool // commit id -> file set
	fileList map[string][]string
	rulesets map[int][]Rule
	curFiles map[string]bool
	curRules []Rule
	mat      map[string]bool
	mods     map[string]bool
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func joinList(s []string) string { return strings.Join(s, ",") }

// TestAgainstNaiveModel drives random commits, rulesets, applies, mod marks
// and queries, comparing every observable result with the naive model.
func TestAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1515))

	// Universe: dirs {a,b,c} up to depth 2, files {f,g,h} up to depth 3.
	var allDirs, allFiles []string
	names := []string{"a", "b", "c"}
	fnames := []string{"f", "g", "h"}
	for _, d1 := range names {
		allDirs = append(allDirs, d1)
		for _, fn := range fnames {
			allFiles = append(allFiles, d1+"/"+fn)
		}
		for _, d2 := range names {
			allDirs = append(allDirs, d1+"/"+d2)
			for _, fn := range fnames {
				allFiles = append(allFiles, d1+"/"+d2+"/"+fn)
			}
		}
	}
	for _, fn := range fnames {
		allFiles = append(allFiles, fn)
	}
	allPaths := append(append([]string{}, allDirs...), allFiles...)

	randomDir := func() string { return allDirs[rng.Intn(len(allDirs))] }
	randomPath := func() string { return allPaths[rng.Intn(len(allPaths))] }
	randomPattern := func() string {
		switch rng.Intn(5) {
		case 0:
			return randomPath()
		case 1:
			return randomDir() + "/"
		case 2:
			return randomDir() + "/*"
		case 3:
			return "*"
		default:
			return randomPath()
		}
	}
	randomRules := func() []Rule {
		n := rng.Intn(7)
		rules := make([]Rule, 0, n)
		for i := 0; i < n; i++ {
			pat := randomPattern()
			if rng.Intn(10) == 0 { // inject malformed pattern
				pat = []string{"", "a//b", "a/../b", "x/*/y"}[rng.Intn(4)]
			}
			r := Rule{Action: Action(rng.Intn(2) == 0), Pattern: pat}
			rules = append(rules, r)
			if rng.Intn(10) == 0 && len(rules) > 1 { // inject duplicate
				rules = append(rules, rules[rng.Intn(len(rules))])
			}
		}
		return rules
	}

	e := NewEngine()
	ms := &modelState{
		commits:  map[string]map[string]bool{},
		fileList: map[string][]string{},
		rulesets: map[int][]Rule{},
		curFiles: map[string]bool{},
		mat:      map[string]bool{},
		mods:     map[string]bool{},
	}

	// Register 4 random commits.
	commitIDs := []string{}
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("c%d", i)
		var files []string
		for _, f := range allFiles {
			if rng.Intn(2) == 0 {
				files = append(files, f)
			}
		}
		if len(files) == 0 {
			files = append(files, allFiles[0])
		}
		sort.Strings(files)
		if err := e.AddCommit(id, files); err != nil {
			t.Fatalf("AddCommit(%s): %v", id, err)
		}
		set := map[string]bool{}
		for _, f := range files {
			set[f] = true
		}
		ms.commits[id] = set
		ms.fileList[id] = files
		commitIDs = append(commitIDs, id)
	}
	t.Logf("input: universe of %d dirs, %d files; commits c0..c3 random subsets", len(allDirs), len(allFiles))

	// One valid ruleset to start from.
	v0, err := e.AddRuleset([]Rule{Rule{Include, "*"}})
	if err != nil {
		t.Fatal(err)
	}
	ms.rulesets[v0] = []Rule{Rule{Include, "*"}}

	compareQueries := func(iter int) {
		got, err := e.ListMaterialized("")
		if err != nil {
			t.Fatalf("iter %d: ListMaterialized: %v", iter, err)
		}
		want := sortedSet(ms.mat)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iter %d: ListMaterialized = %v, model wants %v", iter, got, want)
		}
		for _, p := range append(append([]string{}, allPaths...), "zz", "zz/q") {
			gotSt, err := e.QueryPath(p)
			if err != nil {
				t.Fatalf("iter %d: QueryPath(%q): %v", iter, p, err)
			}
			wantSt := naiveQuery(ms.curFiles, ms.curRules, ms.mat, p)
			if gotSt != wantSt {
				t.Fatalf("iter %d: QueryPath(%q) = %v, model wants %v", iter, p, gotSt, wantSt)
			}
		}
	}

	for iter := 0; iter < 400; iter++ {
		switch rng.Intn(4) {
		case 0: // add a ruleset
			rules := randomRules()
			v, err := e.AddRuleset(rules)
			if !modelRulesetValid(rules) {
				if !errors.Is(err, ErrInvalidParam) {
					t.Fatalf("iter %d: AddRuleset(%v) err = %v, want ErrInvalidParam", iter, rules, err)
				}
				t.Logf("iter %d: AddRuleset(%v) rejected as invalid (依据: 参数非法整组拒绝)", iter, rules)
				continue
			}
			if err != nil {
				t.Fatalf("iter %d: AddRuleset(%v) unexpected err %v", iter, rules, err)
			}
			ms.rulesets[v] = rules
			t.Logf("iter %d: AddRuleset(%v) -> version %d", iter, rules, v)

		case 1: // apply (possibly combined commit+ruleset change)
			cid := ""
			if rng.Intn(10) > 0 {
				cid = commitIDs[rng.Intn(len(commitIDs))]
			} else {
				cid = "nope"
			}
			ver := 0
			switch rng.Intn(10) {
			case 0:
				ver = 999 // unknown version
			case 1, 2:
				ver = 0 // keep current
			default:
				for k := range ms.rulesets {
					if rng.Intn(2) == 0 {
						ver = k
						break
					}
				}
			}
			force := rng.Intn(2) == 0
			res, err := e.Apply(cid, ver, force)
			t.Logf("iter %d: Apply(%q, %d, %v)", iter, cid, ver, force)

			// Model the error precedence: commit, then version, then blocked.
			tgtFiles, commitOK := ms.commits[cid]
			if cid == "" {
				tgtFiles, commitOK = ms.curFiles, true
			}
			if !commitOK {
				if !errors.Is(err, ErrCommitNotFound) {
					t.Fatalf("iter %d: err = %v, want ErrCommitNotFound", iter, err)
				}
				continue
			}
			tgtRules, verOK := ms.rulesets[ver]
			if ver == 0 {
				tgtRules, verOK = ms.curRules, true
			}
			if !verOK {
				if !errors.Is(err, ErrRulesetVersionNotFound) {
					t.Fatalf("iter %d: err = %v, want ErrRulesetVersionNotFound", iter, err)
				}
				continue
			}
			tgt := naiveMaterialized(ms.fileListOf(tgtFiles), tgtRules)
			var removed, added, blocked []string
			for p := range ms.mat {
				if !tgt[p] {
					removed = append(removed, p)
					if ms.mods[p] {
						blocked = append(blocked, p)
					}
				}
			}
			for p := range tgt {
				if !ms.mat[p] {
					added = append(added, p)
				}
			}
			sort.Strings(removed)
			sort.Strings(added)
			sort.Strings(blocked)
			if len(blocked) > 0 && !force {
				var berr *BlockedError
				if !errors.As(err, &berr) {
					t.Fatalf("iter %d: err = %v, want *BlockedError(%v)", iter, err, blocked)
				}
				if !reflect.DeepEqual(berr.Paths, blocked) {
					t.Fatalf("iter %d: blocked = %v, model wants %v", iter, berr.Paths, blocked)
				}
				t.Logf("iter %d: blocked as expected on %v (依据: 受阻列表完整)", iter, blocked)
				continue // state must be unchanged; verified by compareQueries
			}
			if err != nil {
				t.Fatalf("iter %d: unexpected err %v", iter, err)
			}
			if joinList(res.Added) != joinList(added) || joinList(res.Removed) != joinList(removed) {
				t.Fatalf("iter %d: added=%v removed=%v, model wants added=%v removed=%v",
					iter, res.Added, res.Removed, added, removed)
			}
			if joinList(res.Discarded) != joinList(blocked) {
				t.Fatalf("iter %d: discarded=%v, model wants %v", iter, res.Discarded, blocked)
			}
			kept := 0
			for p := range ms.mat {
				if tgt[p] {
					kept++
				}
			}
			if res.Kept != kept {
				t.Fatalf("iter %d: kept=%d, model wants %d", iter, res.Kept, kept)
			}
			ms.curFiles = tgtFiles
			ms.curRules = tgtRules
			ms.mat = tgt
			for _, p := range removed {
				delete(ms.mods, p)
			}
			t.Logf("iter %d: applied; added=%d removed=%d kept=%d discarded=%v",
				iter, len(added), len(removed), kept, blocked)

		case 2: // set/clear a local-modification mark
			p := randomPath()
			mod := rng.Intn(2) == 0
			err := e.SetLocalModified(p, mod)
			if ms.mat[p] {
				if err != nil {
					t.Fatalf("iter %d: SetLocalModified(%q,%v): %v", iter, p, mod, err)
				}
				if mod {
					ms.mods[p] = true
				} else {
					delete(ms.mods, p)
				}
			} else if !errors.Is(err, ErrNotMaterialized) {
				t.Fatalf("iter %d: SetLocalModified(%q) err = %v, want ErrNotMaterialized", iter, p, err)
			}
			t.Logf("iter %d: SetLocalModified(%q, %v) -> %v", iter, p, mod, err)

		case 3: // full query comparison
			compareQueries(iter)
			t.Logf("iter %d: queries match model (%d materialized paths)", iter, len(ms.mat))
		}
	}
	compareQueries(999)
	t.Logf("final: %d materialized paths, %d modified; engine == naive model (依据: 随机树+随机规则同朴素模型比对)",
		len(ms.mat), len(ms.mods))
}

// fileListOf reconstructs a sorted file list from a set (commits are
// unordered in the model; naiveMaterialized is order-insensitive).
func (ms *modelState) fileListOf(set map[string]bool) []string {
	return sortedSet(set)
}
