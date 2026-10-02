package match

func expansionCount(pattern Pattern) int {
	switch p := pattern.(type) {
	case Wildcard:
		return 1
	case ConstructorPattern:
		count := 1
		for _, child := range p.Children {
			count *= expansionCount(child)
		}
		return count
	case OrPattern:
		count := 0
		for _, branch := range p.Branches {
			count += expansionCount(branch)
		}
		return count
	default:
		return 0
	}
}

func expandPattern(pattern Pattern) [][]Pattern {
	switch p := pattern.(type) {
	case Wildcard:
		return [][]Pattern{{Wildcard{}}}
	case ConstructorPattern:
		childrenRows := make([][][]Pattern, len(p.Children))
		for i, child := range p.Children {
			childrenRows[i] = expandPattern(child)
		}
		return combineConstructor(p.Name, childrenRows, 0, nil)
	case OrPattern:
		var rows [][]Pattern
		for _, branch := range p.Branches {
			rows = append(rows, expandPattern(branch)...)
		}
		return rows
	default:
		return nil
	}
}

func combineConstructor(name string, childrenRows [][][]Pattern, index int, prefix []Pattern) [][]Pattern {
	if index == len(childrenRows) {
		return [][]Pattern{{ConstructorPattern{Name: name, Children: append([]Pattern(nil), prefix...)}}}
	}
	var rows [][]Pattern
	for _, childRow := range childrenRows[index] {
		nextPrefix := append(append([]Pattern(nil), prefix...), childRow...)
		rows = append(rows, combineConstructor(name, childrenRows, index+1, nextPrefix)...)
	}
	return rows
}
