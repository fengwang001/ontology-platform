package bptree

// Independent "from the spec" reference bulk loader used to cross-check the
// streaming Loader for every key count from 0 to 200.

type refPage struct {
	keys     []string
	children []*refPage
	leaf     bool
	minKey   string
}

type refTree struct {
	levels [][]*refPage
	root   *refPage
	height int
}

func ceilDiv(a, b int) int { return (a + b - 1) / b }

func naiveLoad(C, B, p int, keys []string) *refTree {
	mL := C / 2
	mI := (B + 1) / 2
	tL := ceilDiv(C*p, 100)
	if tL < mL {
		tL = mL
	}
	tI := ceilDiv(B*p, 100)
	if tI < mI {
		tI = mI
	}

	t := &refTree{}
	if len(keys) == 0 {
		empty := &refPage{leaf: true}
		t.levels = [][]*refPage{{empty}}
		t.root = empty
		t.height = 1
		return t
	}

	// Streaming leaf sealing: close at tL, next page starts at next Add.
	var level []*refPage
	for i := 0; i < len(keys); {
		j := i + tL
		if j > len(keys) {
			j = len(keys)
		}
		level = append(level, refLeaf(keys[i:j]))
		i = j
	}

	// Finish: rebalance the last leaf if underfull.
	level = refRebalanceLeaves(level, C, mL)
	t.levels = [][]*refPage{level}

	for len(level) > 1 {
		level = refBuildInternal(level, tI, mI, B)
		t.levels = append(t.levels, level)
	}
	t.root = level[0]
	t.height = len(t.levels)
	return t
}

func refLeaf(keys []string) *refPage {
	p := &refPage{leaf: true, keys: append([]string(nil), keys...)}
	if len(keys) > 0 {
		p.minKey = keys[0]
	}
	return p
}

func refRebalanceLeaves(level []*refPage, C, mL int) []*refPage {
	if len(level) < 2 {
		return level
	}
	last := level[len(level)-1]
	if len(last.keys) >= mL {
		return level
	}
	prev := level[len(level)-2]
	joined := make([]string, 0, len(prev.keys)+len(last.keys))
	joined = append(joined, prev.keys...)
	joined = append(joined, last.keys...)
	if len(joined) <= C {
		level[len(level)-2] = refLeaf(joined)
		return level[:len(level)-1]
	}
	mid := (len(joined) + 1) / 2
	level[len(level)-2] = refLeaf(joined[:mid])
	level[len(level)-1] = refLeaf(joined[mid:])
	return level
}

func refBuildInternal(children []*refPage, tI, mI, B int) []*refPage {
	bounds := refGroup(len(children), tI, mI, B)
	parents := make([]*refPage, 0, len(bounds))
	for _, g := range bounds {
		kids := children[g[0]:g[1]]
		p := &refPage{
			leaf:     false,
			children: append([]*refPage(nil), kids...),
			minKey:   kids[0].minKey,
		}
		p.keys = make([]string, len(kids)-1)
		for j := 1; j < len(kids); j++ {
			p.keys[j-1] = kids[j].minKey
		}
		parents = append(parents, p)
	}
	return parents
}

func refGroup(n, t, min, cap int) [][2]int {
	var groups [][2]int
	for i := 0; i < n; {
		j := i + t
		if j > n {
			j = n
		}
		groups = append(groups, [2]int{i, j})
		i = j
	}
	if len(groups) >= 2 {
		last := groups[len(groups)-1]
		if last[1]-last[0] < min {
			prev := groups[len(groups)-2]
			sum := last[1] - prev[0]
			if sum <= cap {
				groups[len(groups)-2] = [2]int{prev[0], last[1]}
				groups = groups[:len(groups)-1]
			} else {
				mid := prev[0] + (sum+1)/2
				groups[len(groups)-2] = [2]int{prev[0], mid}
				groups[len(groups)-1] = [2]int{mid, last[1]}
			}
		}
	}
	return groups
}

func refGet(t *refTree, key string) (bool, int) {
	cur := t.root
	visited := 0
	for {
		visited++
		if cur.leaf {
			for _, k := range cur.keys {
				if k == key {
					return true, visited
				}
			}
			return false, visited
		}
		idx := 0
		for idx < len(cur.keys) && key >= cur.keys[idx] {
			idx++
		}
		cur = cur.children[idx]
	}
}
