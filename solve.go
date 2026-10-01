package depsolver

import (
	"context"
	"sort"
)

// solveAt 在给定快照上执行固定次序回溯求解。
func solveAt(ctx context.Context, snap *Snapshot, root map[string]Range, rootOrder []string) (map[string]string, error) {
	log := loggerFromContext(ctx)
	log.Logf("solve: root=%v", rootOrder)

	constraints := map[string][]Range{}
	constrained := map[string]bool{}
	for _, name := range rootOrder {
		constraints[name] = append(constraints[name], root[name])
		constrained[name] = true
	}

	chosen := map[string]Version{}

	var dfs func() bool
	dfs = func() bool {
		name := smallestConstrainedUndecided(constrained, chosen)
		if name == "" {
			return true
		}
		candidates := snap.candidateVersions(name)
		log.Logf("decide: package=%s candidates=%v", name, versionStrings(candidates))
		for _, v := range candidates {
			if !allRangesAccept(v, constraints[name]) {
				log.Logf("reject: %s@%s violates constraints", name, v.raw)
				continue
			}
			deps, depNames := snap.dependencies(name, v)
			missing := false
			for _, dep := range depNames {
				if _, exists := snap.packages[dep]; !exists {
					log.Logf("reject: %s@%s missing dep package %s", name, v.raw, dep)
					missing = true
					break
				}
			}
			if missing {
				continue
			}
			if !depsCompatibleWithChosen(deps, depNames, chosen) {
				log.Logf("reject: %s@%s conflicts with already chosen packages", name, v.raw)
				continue
			}
			if selfRange, ok := deps[name]; ok && !selfRange.contains(v) {
				log.Logf("reject: %s@%s violates its own dependency range", name, v.raw)
				continue
			}
			chosen[name] = v
			for _, dep := range depNames {
				constraints[dep] = append(constraints[dep], deps[dep])
				constrained[dep] = true
			}
			log.Logf("try: %s@%s adds deps=%v", name, v.raw, depNames)
			if dfs() {
				return true
			}
			log.Logf("backtrack: %s@%s", name, v.raw)
			for _, dep := range depNames {
				constraints[dep] = constraints[dep][:len(constraints[dep])-1]
				if len(constraints[dep]) == 0 {
					delete(constrained, dep)
				}
			}
			delete(chosen, name)
		}
		return false
	}

	if dfs() {
		result := map[string]string{}
		names := make([]string, 0, len(chosen))
		for name, v := range chosen {
			result[name] = v.raw
			names = append(names, name)
		}
		sort.Strings(names)
		log.Logf("solve: solution=%v", result)
		return result, nil
	}

	if missing := firstMissingPackage(snap, root, rootOrder); missing != "" {
		log.Logf("solve: package-not-found=%s", missing)
		return nil, ErrPackageNotFound
	}
	log.Logf("solve: no-solution")
	return nil, ErrNoSolution
}

func depsCompatibleWithChosen(deps map[string]Range, depNames []string, chosen map[string]Version) bool {
	for _, dep := range depNames {
		if fixed, ok := chosen[dep]; ok && !deps[dep].contains(fixed) {
			return false
		}
	}
	return true
}

func smallestConstrainedUndecided(constrained map[string]bool, chosen map[string]Version) string {
	var best string
	for name := range constrained {
		if _, ok := chosen[name]; ok {
			continue
		}
		if best == "" || name < best {
			best = name
		}
	}
	return best
}

func allRangesAccept(v Version, ranges []Range) bool {
	for _, rng := range ranges {
		if !rng.contains(v) {
			return false
		}
	}
	return true
}

func (s *Snapshot) candidateVersions(name string) []Version {
	out := []Version{}
	for raw, pv := range s.packages[name] {
		if s.yanked[name][raw] {
			continue
		}
		v, err := ParseVersion(pv.Version)
		if err != nil {
			continue
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		return CompareVersion(out[i], out[j]) > 0
	})
	return out
}

func (s *Snapshot) dependencies(name string, v Version) (map[string]Range, []string) {
	pv := s.packages[name][v.raw]
	deps := map[string]Range{}
	names := make([]string, 0, len(pv.Dependencies))
	for dep, text := range pv.Dependencies {
		rng, err := ParseRange(text)
		if err != nil {
			continue
		}
		deps[dep] = rng
		names = append(names, dep)
	}
	sort.Strings(names)
	return deps, names
}

func versionStrings(vs []Version) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.raw
	}
	return out
}

// firstMissingPackage 按名字字节序找到根可达闭包中首个仓库缺失的包。
func firstMissingPackage(snap *Snapshot, root map[string]Range, rootOrder []string) string {
	seen := map[string]bool{}
	var walk func(name string) string
	walk = func(name string) string {
		if seen[name] {
			return ""
		}
		seen[name] = true
		if snap.packages[name] == nil {
			return name
		}
		depSet := map[string]bool{}
		for _, pv := range snap.packages[name] {
			for dep := range pv.Dependencies {
				depSet[dep] = true
			}
		}
		deps := make([]string, 0, len(depSet))
		for dep := range depSet {
			deps = append(deps, dep)
		}
		sort.Strings(deps)
		for _, dep := range deps {
			if missing := walk(dep); missing != "" {
				return missing
			}
		}
		return ""
	}
	for _, name := range rootOrder {
		if missing := walk(name); missing != "" {
			return missing
		}
	}
	return ""
}

// bruteForceSolve 独立枚举全部可行组合，用同一决策序列的候选序号选出首个解。
func bruteForceSolve(snap *Snapshot, root map[string]Range, rootOrder []string) (map[string]string, error) {
	if missing := firstMissingPackage(snap, root, rootOrder); missing != "" {
		return nil, ErrPackageNotFound
	}

	pkgNames := make([]string, 0, len(snap.packages))
	for name := range snap.packages {
		pkgNames = append(pkgNames, name)
	}
	sort.Strings(pkgNames)

	var best map[string]Version
	var bestKey []int
	assignment := map[string]Version{}

	var enumerate func(int)
	enumerate = func(idx int) {
		if idx == len(pkgNames) {
			if feasibleAssignment(root, rootOrder, snap, assignment) {
				key := decisionKey(rootOrder, snap, assignment)
				if best == nil || compareIntSlice(key, bestKey) < 0 {
					bestKey = key
					best = make(map[string]Version, len(assignment))
					for name, v := range assignment {
						best[name] = v
					}
				}
			}
			return
		}
		name := pkgNames[idx]
		candidates := snap.candidateVersions(name)
		enumerate(idx + 1)
		for i := len(candidates) - 1; i >= 0; i-- {
			assignment[name] = candidates[i]
			enumerate(idx + 1)
			delete(assignment, name)
		}
	}
	enumerate(0)

	if best == nil {
		return nil, ErrNoSolution
	}
	result := map[string]string{}
	for name, v := range best {
		result[name] = v.raw
	}
	return result, nil
}

// feasibleAssignment 判定赋值是否为完整可行解：所有受约束包均被选中且全部范围满足。
func feasibleAssignment(root map[string]Range, rootOrder []string, snap *Snapshot, assignment map[string]Version) bool {
	constrained := map[string]bool{}
	for _, name := range rootOrder {
		v, ok := assignment[name]
		if !ok || !root[name].contains(v) {
			return false
		}
		constrained[name] = true
	}

	checked := map[string]bool{}
	for {
		name := ""
		for cand := range constrained {
			if !checked[cand] && (name == "" || cand < name) {
				name = cand
			}
		}
		if name == "" {
			break
		}
		checked[name] = true
		v := assignment[name]
		pv := snap.packages[name][v.raw]
		deps := make([]string, 0, len(pv.Dependencies))
		for dep := range pv.Dependencies {
			deps = append(deps, dep)
		}
		sort.Strings(deps)
		for _, dep := range deps {
			dv, ok := assignment[dep]
			if !ok {
				return false
			}
			rng, err := ParseRange(pv.Dependencies[dep])
			if err != nil || !rng.contains(dv) {
				return false
			}
			constrained[dep] = true
		}
	}
	return true
}

// decisionKey 模拟固定搜索次序，返回每个决策点选中版本在高到低候选列表中的序号。
func decisionKey(rootOrder []string, snap *Snapshot, assignment map[string]Version) []int {
	key := []int{}
	constrained := map[string]bool{}
	keyed := map[string]bool{}
	for _, name := range rootOrder {
		constrained[name] = true
	}
	for {
		name := ""
		for cand := range constrained {
			if !keyed[cand] && (name == "" || cand < name) {
				name = cand
			}
		}
		if name == "" {
			break
		}
		v := assignment[name]
		rank := -1
		for i, c := range snap.candidateVersions(name) {
			if c.raw == v.raw {
				rank = i
			}
		}
		key = append(key, rank)
		keyed[name] = true
		for dep := range snap.packages[name][v.raw].Dependencies {
			constrained[dep] = true
		}
	}
	return key
}

func compareIntSlice(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := compareInt(a[i], b[i]); c != 0 {
			return c
		}
	}
	return compareInt(len(a), len(b))
}
