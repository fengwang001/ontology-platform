package ontology

import "sort"

// rightRenames maps each right-side non-key column to its output name:
// "right.<name>" when the name collides with a left column, else <name>.
// It also returns the output names in sorted order.
func rightRenames(left, right []map[string]any, keys []string) (map[string]string, []string) {
	keySet := make(map[string]bool, len(keys))
	for _, k := range keys {
		keySet[k] = true
	}
	leftNames := make(map[string]bool)
	for _, row := range left {
		for name := range row {
			leftNames[name] = true
		}
	}
	seen := make(map[string]bool)
	renames := make(map[string]string)
	var cols []string
	for _, row := range right {
		for name := range row {
			if keySet[name] || seen[name] {
				continue
			}
			seen[name] = true
			outName := name
			if leftNames[name] {
				outName = "right." + name
			}
			renames[name] = outName
			cols = append(cols, outName)
		}
	}
	sort.Strings(cols)
	return renames, cols
}

// buildRow deep-copies the left row and merges the right row's non-key
// columns under their output names. A nil right row (unmatched Left) adds
// nothing, so right columns stay missing rather than zero-valued.
func buildRow(lrow, rrow map[string]any, keys []string, renames map[string]string) map[string]any {
	out := deepCopy(lrow)
	if rrow == nil {
		return out
	}
	keySet := make(map[string]bool, len(keys))
	for _, k := range keys {
		keySet[k] = true
	}
	for name, v := range rrow {
		if keySet[name] {
			continue
		}
		if outName, ok := renames[name]; ok {
			out[outName] = deepCopyValue(v)
		}
	}
	return out
}
