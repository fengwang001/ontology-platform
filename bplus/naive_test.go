package bplus

type naivePage struct {
	keys     []string
	children []int
	min      string
}

type naiveTree struct {
	levels [][]naivePage
}

func naiveBulkLoad(keys []string, leafTarget, leafCapacity, internalTarget, internalCapacity int) naiveTree {
	var groups [][]string
	for start := 0; start < len(keys); {
		end := min(start+leafTarget, len(keys))
		groups = append(groups, append([]string{}, keys[start:end]...))
		start = end
	}
	groups = rebalanceNaiveLeaves(groups, leafCapacity/2, leafCapacity)

	var leaves []naivePage
	for _, group := range groups {
		leaves = append(leaves, naivePage{keys: group, min: firstKey(group)})
	}
	if len(leaves) == 0 {
		leaves = []naivePage{{}}
	}
	levels := [][]naivePage{leaves}

	internalMin := (internalCapacity + 1) / 2
	for len(levels[len(levels)-1]) != 1 {
		lower := levels[len(levels)-1]
		var pages []naivePage
		for start := 0; start < len(lower); {
			end := min(start+internalTarget, len(lower))
			pages = append(pages, makeNaiveInternal(lower, start, end))
			start = end
		}
		pages = rebalanceNaiveInternal(pages, lower, internalMin, internalCapacity)
		levels = append(levels, pages)
	}
	return naiveTree{levels: levels}
}

func rebalanceNaiveLeaves(groups [][]string, minimum, capacity int) [][]string {
	if len(groups) < 2 || len(groups[len(groups)-1]) >= minimum {
		return groups
	}
	combined := append(append([]string{}, groups[len(groups)-2]...), groups[len(groups)-1]...)
	if len(combined) <= capacity {
		groups[len(groups)-2] = combined
		return groups[:len(groups)-1]
	}
	split := (len(combined) + 1) / 2
	groups[len(groups)-2] = combined[:split]
	groups[len(groups)-1] = combined[split:]
	return groups
}

func makeNaiveInternal(children []naivePage, start, end int) naivePage {
	page := naivePage{children: make([]int, 0, end-start)}
	page.min = children[start].min
	for index := start; index < end; index++ {
		page.children = append(page.children, index)
	}
	for index := start + 1; index < end; index++ {
		page.keys = append(page.keys, naiveMinimum(children[index]))
	}
	return page
}

func rebalanceNaiveInternal(pages []naivePage, lower []naivePage, minimum, capacity int) []naivePage {
	if len(pages) <= 1 || len(pages[len(pages)-1].children) >= minimum {
		return pages
	}

	left := pages[len(pages)-2]
	right := pages[len(pages)-1]
	total := len(left.children) + len(right.children)
	var keys []string
	keys = append(keys, left.keys...)
	keys = append(keys, lower[right.children[0]].min)
	keys = append(keys, right.keys...)
	children := append(append([]int{}, left.children...), right.children...)

	if total <= capacity {
		pages[len(pages)-2] = naivePage{keys: keys, children: children, min: lower[children[0]].min}
		return pages[:len(pages)-1]
	}

	split := (total + 1) / 2
	pages[len(pages)-2] = naivePage{
		keys:     append([]string{}, keys[:split-1]...),
		children: append([]int{}, children[:split]...),
		min:      lower[children[0]].min,
	}
	pages[len(pages)-1] = naivePage{
		keys:     append([]string{}, keys[split:]...),
		children: append([]int{}, children[split:]...),
		min:      lower[children[split]].min,
	}
	return pages
}

func naiveMinimum(page naivePage) string {
	return page.min
}

func firstKey(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}
