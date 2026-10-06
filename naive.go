package ontology

import "sort"

// naiveState 是独立编写的朴素对照模型：
// 每个文件线性扫描全部规则取最后命中，再补全祖先目录、
// 剔除无文件后代的空目录。逻辑刻意直白，与引擎的索引/剪枝实现互不共享。
type naiveState struct {
	files map[string]bool
	rules []Rule
	dirty map[string]bool
}

func newNaive(files []string, rules []Rule) *naiveState {
	n := &naiveState{
		files: map[string]bool{},
		rules: rules,
		dirty: map[string]bool{},
	}
	for _, f := range files {
		n.files[f] = true
	}
	return n
}

// naiveHit 对单路径线性扫描全部规则，返回最后命中动作；无命中为 false。
func naiveHit(rules []Rule, path string) (Action, bool) {
	segs := splitPath(path)
	hit := false
	action := Exclude
	for _, r := range rules {
		if naiveMatch(r.Pattern, segs, path) {
			hit = true
			action = r.Action
		}
	}
	return action, hit
}

// naiveMatch 以最直白的字符串方式判断模式是否命中路径。
func naiveMatch(pattern string, segs []string, path string) bool {
	if len(pattern) > 0 && pattern[len(pattern)-1] == '/' {
		dir := pattern[:len(pattern)-1]
		if path == dir {
			return true
		}
		return len(path) > len(dir)+1 && path[:len(dir)+1] == dir+"/"
	}
	ps := splitPath(pattern)
	if ps[len(ps)-1] == "*" {
		if len(segs) != len(ps) {
			return false
		}
		for i := 0; i < len(ps)-1; i++ {
			if ps[i] != segs[i] {
				return false
			}
		}
		return true
	}
	return pattern == path
}

// naiveMaterializedFiles 返回朴素物化文件集合（已排序）。
func (n *naiveState) materializedFiles() []string {
	var out []string
	for f := range n.files {
		if a, hit := naiveHit(n.rules, f); hit && a == Include {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// naiveMaterialized 返回朴素物化的全部路径（文件 + 祖先目录，已排序）。
func naiveMaterialized(files []string, rules []Rule) []string {
	n := newNaive(files, rules)
	set := map[string]bool{}
	for _, f := range n.materializedFiles() {
		set[f] = true
		segs := splitPath(f)
		for i := 1; i < len(segs); i++ {
			set[joinSegs(segs[:i])] = true
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// naiveClassify 朴素计算某路径的五种查询结果。
func naiveClassify(files []string, rules []Rule, path string) PathStatus {
	n := newNaive(files, rules)
	exists := false
	isFile := n.files[path]
	if isFile {
		exists = true
	} else {
		pfx := path + "/"
		for f := range n.files {
			if len(f) > len(pfx) && f[:len(pfx)] == pfx {
				exists = true
				break
			}
		}
	}
	if !exists {
		return StatusNotInCommit
	}
	matSet := map[string]bool{}
	for _, f := range n.materializedFiles() {
		matSet[f] = true
		segs := splitPath(f)
		for i := 1; i < len(segs); i++ {
			matSet[joinSegs(segs[:i])] = true
		}
	}
	if matSet[path] {
		return StatusMaterialized
	}
	if a, hit := naiveHit(rules, path); hit && a == Exclude {
		return StatusExcludedByRule
	}
	if isFile {
		return StatusNoRuleMatch
	}
	if n.subtreeHasInclude(splitPath(path)) {
		return StatusEmptyDirectory
	}
	return StatusNoRuleMatch
}

// subtreeHasInclude 直白判断目录自身或任意后代是否存在「最终包含」命中。
// 候选路径 = 该目录子树内的真实文件 + 规则模式真正指向的路径，
// 每个候选都用 naiveHit 取有序规则的最后裁决。
func (n *naiveState) subtreeHasInclude(dirSegs []string) bool {
	dir := joinSegs(dirSegs)
	pfx := ""
	if dir != "" {
		pfx = dir + "/"
	}
	inSubtree := func(p string) bool {
		if p == dir {
			return true
		}
		return pfx != "" && len(p) >= len(pfx) && p[:len(pfx)] == pfx
	}
	check := func(p string) bool {
		a, hit := naiveHit(n.rules, p)
		return hit && a == Include
	}

	candidates := map[string]bool{dir: true}
	// 真实文件路径（含其所有祖先目录，目录也可能被规则直接命中）。
	for f := range n.files {
		if !inSubtree(f) {
			continue
		}
		candidates[f] = true
		segs := splitPath(f)
		for i := 1; i < len(segs); i++ {
			anc := joinSegs(segs[:i])
			if inSubtree(anc) {
				candidates[anc] = true
			}
		}
	}
	// 规则模式指向的路径。
	for _, r := range n.rules {
		switch {
		case isChildStar(r.Pattern):
			segs := splitPath(r.Pattern)
			wd := joinSegs(segs[:len(segs)-1])
			// 只有当通配的作用目录就在该目录子树内时，
			// 其直接子项才可能落在该子树。
			if wd != dir && !(len(wd) >= len(pfx) && wd[:len(pfx)] == pfx) {
				continue
			}
			// 只枚举 wd 下真实存在的直接子项（文件或目录）。
			for f := range n.files {
				if rel, ok := relUnder(f, wd); ok {
					name := rel
					if i := indexSlash(rel); i >= 0 {
						name = rel[:i]
					}
					cp := childPath(wd, name)
					if inSubtree(cp) {
						candidates[cp] = true
					}
				}
			}
		default:
			target := patternTargetPath(r.Pattern)
			if inSubtree(target) {
				candidates[target] = true
			}
		}
	}
	for cand := range candidates {
		if check(cand) {
			return true
		}
	}
	return false
}

// patternTargetPath 返回模式「主要作用目标」的路径：
// 前缀规则返回其目录，通配返回父目录/占位，精确返回自身。
// 判定目录空状态时只需知道目标是否落在目录子树内。
func patternTargetPath(pattern string) string {
	if len(pattern) > 0 && pattern[len(pattern)-1] == '/' {
		return pattern[:len(pattern)-1]
	}
	segs := splitPath(pattern)
	if segs[len(segs)-1] == "*" {
		return joinSegs(segs[:len(segs)-1])
	}
	return pattern
}

func isChildStar(pattern string) bool {
	if len(pattern) > 0 && pattern[len(pattern)-1] == '/' {
		return false
	}
	segs := splitPath(pattern)
	return segs[len(segs)-1] == "*"
}

func relUnder(f, dir string) (string, bool) {
	if dir == "" {
		return f, true
	}
	pfx := dir + "/"
	if len(f) > len(pfx) && f[:len(pfx)] == pfx {
		return f[len(pfx):], true
	}
	return "", false
}

func indexSlash(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}
