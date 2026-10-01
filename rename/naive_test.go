package rename

import "sort"

// naiveDetect is a straightforward implementation of the spec without the
// size-ratio pruning: it computes C for every surviving (deleted, added)
// pair in phase 2. It also reports how many of those pairs the bound would
// prune, so tests can prove the real detector computes C exactly on the
// unpruned pairs.
func naiveDetect(deleted, added map[string]string, threshold int) (Result, int, int) {
	delUsed := map[string]bool{}
	addUsed := map[string]bool{}
	var renames []Rename

	// Phase 1: exact matches, grouped by identical content.
	delByContent := map[string][]string{}
	addByContent := map[string][]string{}
	for path, content := range deleted {
		delByContent[content] = append(delByContent[content], path)
	}
	for path, content := range added {
		addByContent[content] = append(addByContent[content], path)
	}
	contentKeys := make([]string, 0, len(delByContent))
	for content := range delByContent {
		contentKeys = append(contentKeys, content)
	}
	sort.Strings(contentKeys)
	for _, content := range contentKeys {
		delPaths := delByContent[content]
		addPaths := addByContent[content]
		if len(addPaths) == 0 {
			continue
		}
		delGroups := map[string][]string{}
		addGroups := map[string][]string{}
		for _, p := range delPaths {
			b := baseName(p)
			delGroups[b] = append(delGroups[b], p)
		}
		for _, p := range addPaths {
			b := baseName(p)
			addGroups[b] = append(addGroups[b], p)
		}
		names := make([]string, 0, len(delGroups))
		for b := range delGroups {
			if len(addGroups[b]) > 0 {
				names = append(names, b)
			}
		}
		sort.Strings(names)
		for _, b := range names {
			dg := append([]string{}, delGroups[b]...)
			ag := append([]string{}, addGroups[b]...)
			sort.Strings(dg)
			sort.Strings(ag)
			n := len(dg)
			if len(ag) < n {
				n = len(ag)
			}
			for i := 0; i < n; i++ {
				renames = append(renames, Rename{dg[i], ag[i], 100})
				delUsed[dg[i]] = true
				addUsed[ag[i]] = true
			}
		}
		var delLeft, addLeft []string
		for _, p := range delPaths {
			if !delUsed[p] {
				delLeft = append(delLeft, p)
			}
		}
		for _, p := range addPaths {
			if !addUsed[p] {
				addLeft = append(addLeft, p)
			}
		}
		sort.Strings(delLeft)
		sort.Strings(addLeft)
		n := len(delLeft)
		if len(addLeft) < n {
			n = len(addLeft)
		}
		for i := 0; i < n; i++ {
			renames = append(renames, Rename{delLeft[i], addLeft[i], 100})
			delUsed[delLeft[i]] = true
			addUsed[addLeft[i]] = true
		}
	}

	var delLeft, addLeft []string
	for path := range deleted {
		if !delUsed[path] {
			delLeft = append(delLeft, path)
		}
	}
	for path := range added {
		if !addUsed[path] {
			addLeft = append(addLeft, path)
		}
	}
	sort.Strings(delLeft)
	sort.Strings(addLeft)

	type candidate struct {
		score    int
		sameName bool
		delPath  string
		addPath  string
	}
	var candidates []candidate

	cComputed := 0
	prunedByBound := 0
	for _, dp := range delLeft {
		for _, ap := range addLeft {
			ds, asz := len(deleted[dp]), len(added[ap])
			mn, mx := ds, asz
			if mn > mx {
				mn, mx = mx, mn
			}
			if mn*100/mx < threshold {
				prunedByBound++
			}
			// Naive: always compute C, never prune.
			cComputed++
			dh := map[string]int{}
			for _, line := range splitLines(deleted[dp]) {
				dh[line]++
			}
			ah := map[string]int{}
			for _, line := range splitLines(added[ap]) {
				ah[line]++
			}
			small, large := dh, ah
			if len(ah) < len(dh) {
				small, large = ah, dh
			}
			var common int
			for line, c1 := range small {
				c2 := large[line]
				c := c1
				if c2 < c {
					c = c2
				}
				common += c * len(line)
			}
			score := common * 100 / mx
			if score >= threshold {
				candidates = append(candidates, candidate{
					score:    score,
					sameName: baseName(dp) == baseName(ap),
					delPath:  dp,
					addPath:  ap,
				})
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.sameName != b.sameName {
			return a.sameName
		}
		if a.delPath != b.delPath {
			return a.delPath < b.delPath
		}
		return a.addPath < b.addPath
	})
	for _, c := range candidates {
		if delUsed[c.delPath] || addUsed[c.addPath] {
			continue
		}
		renames = append(renames, Rename{c.delPath, c.addPath, c.score})
		delUsed[c.delPath] = true
		addUsed[c.addPath] = true
	}
	sort.Slice(renames, func(i, j int) bool { return renames[i].From < renames[j].From })

	var ud, ua []string
	for _, p := range delLeft {
		if !delUsed[p] {
			ud = append(ud, p)
		}
	}
	for _, p := range addLeft {
		if !addUsed[p] {
			ua = append(ua, p)
		}
	}
	if renames == nil {
		renames = []Rename{}
	}
	if ud == nil {
		ud = []string{}
	}
	if ua == nil {
		ua = []string{}
	}
	return Result{renames, ud, ua}, cComputed, prunedByBound
}
