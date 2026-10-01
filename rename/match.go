package rename

import "sort"

// splitLines splits content into lines the way the detector defines them:
// every line keeps its trailing '\n'; a final line without '\n' is still a
// line; empty content has zero lines.
func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			lines = append(lines, content[start:i+1])
			start = i + 1
		}
	}
	if start < len(content) {
		lines = append(lines, content[start:])
	}
	return lines
}

func baseName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

func (d *Detector) compute() *Result {
	type entry struct {
		path    string
		content string
	}

	delEntries := make([]entry, 0, len(d.deleted))
	addEntries := make([]entry, 0, len(d.added))
	for path, content := range d.deleted {
		delEntries = append(delEntries, entry{path, content})
	}
	for path, content := range d.added {
		addEntries = append(addEntries, entry{path, content})
	}

	delUsed := make(map[string]bool)
	addUsed := make(map[string]bool)
	var renames []Rename

	// Phase 1: exact (byte-identical) matches.
	delByContent := make(map[string][]string)
	addByContent := make(map[string][]string)
	for _, e := range delEntries {
		delByContent[e.content] = append(delByContent[e.content], e.path)
	}
	for _, e := range addEntries {
		addByContent[e.content] = append(addByContent[e.content], e.path)
	}
	for content, delPaths := range delByContent {
		addPaths := addByContent[content]
		if len(addPaths) == 0 {
			continue
		}

		// First pair within same-base-name subgroups.
		delGroups := make(map[string][]string)
		addGroups := make(map[string][]string)
		for _, p := range delPaths {
			b := baseName(p)
			delGroups[b] = append(delGroups[b], p)
		}
		for _, p := range addPaths {
			b := baseName(p)
			addGroups[b] = append(addGroups[b], p)
		}
		for b, dg := range delGroups {
			ag := addGroups[b]
			if len(ag) == 0 {
				continue
			}
			sort.Strings(dg)
			sort.Strings(ag)
			n := len(dg)
			if len(ag) < n {
				n = len(ag)
			}
			for i := 0; i < n; i++ {
				renames = append(renames, Rename{From: dg[i], To: ag[i], Score: 100})
				delUsed[dg[i]] = true
				addUsed[ag[i]] = true
			}
		}

		// Then pair everything left in the content group, regardless of name.
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
			renames = append(renames, Rename{From: delLeft[i], To: addLeft[i], Score: 100})
			delUsed[delLeft[i]] = true
			addUsed[addLeft[i]] = true
		}
	}

	// Phase 2: similarity matches among the survivors.
	var delLeft, addLeft []string
	for _, e := range delEntries {
		if !delUsed[e.path] {
			delLeft = append(delLeft, e.path)
		}
	}
	for _, e := range addEntries {
		if !addUsed[e.path] {
			addLeft = append(addLeft, e.path)
		}
	}
	sort.Strings(delLeft)
	sort.Strings(addLeft)

	// Per-file line histograms, built only for files that reach phase 2.
	delHist := make(map[string]map[string]int, len(delLeft))
	addHist := make(map[string]map[string]int, len(addLeft))
	histogram := func(content string) map[string]int {
		h := make(map[string]int)
		for _, line := range splitLines(content) {
			h[line]++
		}
		return h
	}

	type candidate struct {
		score    int
		sameName bool
		delPath  string
		addPath  string
	}
	var candidates []candidate

	for _, dp := range delLeft {
		dcontent := d.deleted[dp]
		dsize := len(dcontent)
		for _, ap := range addLeft {
			acontent := d.added[ap]
			asize := len(acontent)
			mx := dsize
			mn := asize
			if asize > mx {
				mx = asize
				mn = dsize
			}
			if mx == 0 {
				// Both files are empty; empty contents are identical and
				// would have been paired in phase 1.
				continue
			}
			// C cannot exceed the smaller file, so the score cannot exceed
			// floor(min*100/max). Prune before computing C.
			if mn*100/mx < d.threshold {
				continue
			}

			dh, ok := delHist[dp]
			if !ok {
				dh = histogram(dcontent)
				delHist[dp] = dh
			}
			ah, ok := addHist[ap]
			if !ok {
				ah = histogram(acontent)
				addHist[ap] = ah
			}
			d.commonComputed++

			small, large := dh, ah
			if len(ah) < len(dh) {
				small, large = ah, dh
			}
			var common int
			for line, countSmall := range small {
				countLarge := large[line]
				c := countSmall
				if countLarge < c {
					c = countLarge
				}
				common += c * len(line)
			}
			score := common * 100 / mx
			if score < d.threshold {
				continue
			}
			candidates = append(candidates, candidate{
				score:    score,
				sameName: baseName(dp) == baseName(ap),
				delPath:  dp,
				addPath:  ap,
			})
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
		renames = append(renames, Rename{From: c.delPath, To: c.addPath, Score: c.score})
		delUsed[c.delPath] = true
		addUsed[c.addPath] = true
	}

	var unpairedDeleted, unpairedAdded []string
	for _, p := range delLeft {
		if !delUsed[p] {
			unpairedDeleted = append(unpairedDeleted, p)
		}
	}
	for _, p := range addLeft {
		if !addUsed[p] {
			unpairedAdded = append(unpairedAdded, p)
		}
	}
	sort.Slice(renames, func(i, j int) bool { return renames[i].From < renames[j].From })

	if renames == nil {
		renames = []Rename{}
	}
	if unpairedDeleted == nil {
		unpairedDeleted = []string{}
	}
	if unpairedAdded == nil {
		unpairedAdded = []string{}
	}
	return &Result{
		Renames:         renames,
		UnpairedDeleted: unpairedDeleted,
		UnpairedAdded:   unpairedAdded,
	}
}
