package rename

// splitLines splits content on '\n'. Every line keeps its trailing '\n'
// (including empty "lines" produced by consecutive newlines); a final segment
// without a trailing '\n' is still a line. Empty content yields zero lines.
// Thus "a\n" -> ["a\n"] whereas "a" -> ["a"].
func splitLines(content []byte) []string {
	var lines []string
	start := 0
	for i, b := range content {
		if b == '\n' {
			lines = append(lines, string(content[start:i+1]))
			start = i + 1
		}
	}
	if start < len(content) {
		lines = append(lines, string(content[start:]))
	}
	return lines
}

// commonBytes sums, for every distinct line, min(countDelete, countAdd) times
// the line's byte length.
func commonBytes(delContent, addContent []byte) int {
	delLines := splitLines(delContent)
	addLines := splitLines(addContent)

	counts := make(map[string]int, len(delLines))
	for _, line := range delLines {
		counts[line]++
	}

	common := 0
	seen := make(map[string]bool, len(addLines))
	for _, line := range addLines {
		if seen[line] {
			continue
		}
		seen[line] = true
		addCount := 0
		for _, other := range addLines {
			if other == line {
				addCount++
			}
		}
		delCount := counts[line]
		n := delCount
		if addCount < n {
			n = addCount
		}
		common += n * len(line)
	}
	return common
}

// basename returns the part of path after its last '/', or the whole path.
func basename(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

func sortByPath(entries []fileEntry) {
	// Simple insertion sort keeps inputs tiny in tests while avoiding a second
	// sort.Slice closure allocation; correctness is path byte order.
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j-1].path > entries[j].path; j-- {
			entries[j-1], entries[j] = entries[j], entries[j-1]
		}
	}
}

func removeMatched(entries *[]fileEntry, matched map[string]bool) {
	kept := (*entries)[:0]
	for _, entry := range *entries {
		if !matched[entry.path] {
			kept = append(kept, entry)
		}
	}
	*entries = kept
}
