package dbscan

import (
	"sort"
	"strconv"
)

// clusterSnapshot 是“标签 -> 该簇核心点集合”的快照（仅用于朴素对照与事件测试）。
type clusterSnapshot map[int64]map[int64]bool

func snapshotClusters(core map[int64]bool, label map[int64]int64) clusterSnapshot {
	snap := make(clusterSnapshot)
	for id := range core {
		if !core[id] {
			continue
		}
		lab := label[id]
		if lab == 0 {
			continue
		}
		set := snap[lab]
		if set == nil {
			set = make(map[int64]bool)
			snap[lab] = set
		}
		set[id] = true
	}
	return snap
}

// affectedResult 是一次局部重算的产物。
type affectedResult struct {
	// preComponents/postComponents 是受影响连通分量在变更前/后的核心点集合。
	preComponents  []map[int64]bool
	postComponents []map[int64]bool
	// boundary 是标签可能改变的现存点：受影响点、与身份变化点/受影响分量核心相邻的非核心点。
	boundary map[int64]bool
	// oldLabels 是所有可能改标签的点在变更前的标签（在覆盖标签前采集）。
	oldLabels map[int64]int64
}

// preInfo 是在任何邻接/状态被修改前采集的旧图信息。
type preInfo struct {
	components []map[int64]bool
	oldLabels  map[int64]int64
}

// preMutation 必须在删除点（拆邻接）之前调用：在旧核心图上收集受影响分量，
// 并采集所有可能改标签点的旧标签。
func (s *Service) preMutation(affected, deletionSeeds, goneCores map[int64]bool) *preInfo {
	seeds := make(map[int64]bool)
	extraStarts := make(map[int64]bool)
	addSeed := func(id int64) {
		if s.core[id] {
			seeds[id] = true
		}
	}
	for id := range deletionSeeds {
		addSeed(id)
	}
	for id := range goneCores {
		seeds[id] = true
	}
	for id := range affected {
		// 受影响点若变更后是核心，其所有现存核心邻居所在旧簇都要进入旧侧：
		// 该邻居可能不在新点 eps 圈内，却会经本点在新图中被接通。
		if len(s.adj[id]) >= s.minPts {
			addSeed(id)
			for nb := range s.adj[id] {
				addSeed(nb)
			}
			// 受影响点自身是“即将成核心”的新点（旧身份非核心），它不是旧簇
			// 成员，但仍须作为旧侧起点，才能把其现存核心邻居的旧簇带进事件图。
			if !s.core[id] {
				extraStarts[id] = true
			}
		}
		addSeed(id)
	}
	// 新点本身不是旧核心，但允许它作为 BFS 的“入口”：从它出发，第一步进入
	// 现存旧核心，之后只沿旧核心-旧核心边遍历。
	comps := s.collectPreComponentsSeeds(seeds, extraStarts)

	oldLabels := make(map[int64]int64)
	addOld := func(id int64) { oldLabels[id] = s.label[id] }
	for id := range affected {
		addOld(id)
	}
	for id := range deletionSeeds {
		addOld(id)
	}
	for id := range goneCores {
		addOld(id)
	}
	for _, comp := range comps {
		for cid := range comp {
			addOld(cid)
			for nb := range s.adj[cid] {
				if !s.core[nb] {
					addOld(nb)
				}
			}
		}
	}
	for id := range affected {
		for nb := range s.adj[id] {
			addOld(nb)
		}
	}
	return &preInfo{components: comps, oldLabels: oldLabels}
}

// collectPreComponentsSeeds 在旧核心图上收集分量；extraStarts 是允许的
// 非核心入口点（即将成为核心的新点），它们本身不计入任何旧分量。
func (s *Service) collectPreComponentsSeeds(seeds, extraStarts map[int64]bool) []map[int64]bool {
	visited := make(map[int64]bool)
	var comps []map[int64]bool
	start := func(id int64) {
		if !s.core[id] || visited[id] {
			return
		}
		comp := make(map[int64]bool)
		stack := []int64{id}
		visited[id] = true
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			comp[cur] = true
			for nb := range s.adj[cur] {
				if !s.core[nb] || visited[nb] {
					continue
				}
				visited[nb] = true
				stack = append(stack, nb)
			}
		}
		comps = append(comps, comp)
	}
	for id := range seeds {
		start(id)
	}
	for es := range extraStarts {
		// 从入口点出发，穿过非核心桥接点（它们将被新核心点亮），
		// 命中任一现存旧核心后收集其整个旧簇。
		seen := map[int64]bool{es: true}
		frontier := []int64{es}
		for len(frontier) > 0 {
			cur := frontier[len(frontier)-1]
			frontier = frontier[:len(frontier)-1]
			for nb := range s.adj[cur] {
				if s.core[nb] {
					start(nb)
					continue
				}
				if seen[nb] {
					continue
				}
				seen[nb] = true
				frontier = append(frontier, nb)
			}
		}
	}
	return comps
}

// collectComponents 在由 isCore 判定的核心图上，从 seeds 出发收集所有可达分量。
func (s *Service) collectComponents(seeds map[int64]bool, isCore func(int64) bool) []map[int64]bool {
	visited := make(map[int64]bool)
	var comps []map[int64]bool
	for seed := range seeds {
		if !isCore(seed) || visited[seed] {
			continue
		}
		comp := make(map[int64]bool)
		stack := []int64{seed}
		visited[seed] = true
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			comp[cur] = true
			for nb := range s.adj[cur] {
				if !isCore(nb) || visited[nb] {
					continue
				}
				visited[nb] = true
				stack = append(stack, nb)
			}
		}
		comps = append(comps, comp)
	}
	return comps
}

// recompute 局部重算核心身份与标签。
//   - affected：变更后与插入点/被删点相邻（含其自身）的现存点，邻接已更新；
//   - deletionSeeds：删除前与被删点相邻的现存点（可能在新图中处于任何片段）；
//   - goneCores：本次被删除点中删除前是核心的点（仅存在于旧图）。
func (s *Service) recompute(affectedIn, deletionSeeds map[int64]bool, pre *preInfo) *affectedResult {
	// 受影响集合闭包：以邻域计数达到门槛（变后将是核心）的点为可穿过节点，
	// 从初始受影响点出发扩展。这样“新核心经另一新成核心的桥接点接通远处旧簇”
	// 的链式身份提升也能被重算。删除只减边，deletionSeeds 已给出全部可能降级点。
	affected := make(map[int64]bool)
	{
		frontier := make([]int64, 0)
		push := func(id int64) {
			if !affected[id] {
				affected[id] = true
				frontier = append(frontier, id)
			}
		}
		for id := range affectedIn {
			push(id)
		}
		for len(frontier) > 0 {
			cur := frontier[len(frontier)-1]
			frontier = frontier[:len(frontier)-1]
			// 只有变后将成核心的点才能向邻居传播；非核心点（如新插入的孤立/
			// 边界点）不点亮任何远处簇。
			if len(s.adj[cur]) < s.minPts {
				continue
			}
			for nb := range s.adj[cur] {
				if len(s.adj[nb]) >= s.minPts {
					push(nb)
				}
			}
		}
	}
	newCores := make(map[int64]bool, len(affected))
	changedStatus := make(map[int64]bool)
	for id := range affected {
		nc := len(s.adj[id]) >= s.minPts
		newCores[id] = nc
		if nc != s.core[id] {
			changedStatus[id] = true
		}
	}

	nowCore := func(id int64) bool {
		if nc, touched := newCores[id]; touched {
			return nc
		}
		return s.core[id]
	}

	// 新图种子：删除邻居现存点（若现在是核心）、身份变化点、与身份变化点相邻的现存核心。
	seeds := make(map[int64]bool)
	seedIfCore := func(id int64) {
		if nowCore(id) {
			seeds[id] = true
		}
	}
	for id := range deletionSeeds {
		seedIfCore(id)
	}
	for id := range changedStatus {
		seedIfCore(id)
		for nb := range s.adj[id] {
			seedIfCore(nb)
		}
	}
	// 所有旧受影响分量中仍存活的核心都是新图种子：插入只加边，
	// 它们在新图中可能彼此合并或并入新核心。
	for _, comp := range pre.components {
		for id := range comp {
			seedIfCore(id)
		}
	}
	// 与旧侧对称：身份变化点（尤其新核心）可能经“本变后也是核心的受影响点”
	// 才能触达身份不变的旧簇核心；以所有变后为核心的受影响点为 BFS 起点即可。
	frontier := make([]int64, 0)
	frontSeen := make(map[int64]bool)
	addFront := func(id int64) {
		if nowCore(id) && !frontSeen[id] {
			frontSeen[id] = true
			frontier = append(frontier, id)
		}
	}
	// 身份变化点（含非核心升为核心）都是新图传播起点。
	for id := range affected {
		addFront(id)
	}
	for id := range deletionSeeds {
		addFront(id)
	}
	for len(frontier) > 0 {
		cur := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		seeds[cur] = true
		for nb := range s.adj[cur] {
			addFront(nb)
		}
	}
	postComponents := s.collectComponents(seeds, nowCore)

	// 提交核心身份。
	for id, nc := range newCores {
		s.core[id] = nc
	}
	// 核心点标签 = 分量内最小编号。
	for _, comp := range postComponents {
		minID := int64(0)
		for id := range comp {
			if minID == 0 || id < minID {
				minID = id
			}
		}
		for id := range comp {
			s.label[id] = minID
		}
	}

	// 受影响现存非核心点；身份变化点的现存非核心邻居；受影响分量核心的非核心邻居。
	boundary := make(map[int64]bool)
	for id := range affected {
		if !s.core[id] {
			boundary[id] = true
		}
	}
	for id := range changedStatus {
		for nb := range s.adj[id] {
			if !s.core[nb] {
				boundary[nb] = true
			}
		}
	}
	for _, comp := range postComponents {
		for cid := range comp {
			for nb := range s.adj[cid] {
				if !s.core[nb] {
					boundary[nb] = true
				}
			}
		}
	}
	for id := range boundary {
		minLab := int64(0)
		for nb := range s.adj[id] {
			if !s.core[nb] {
				continue
			}
			lab := s.label[nb]
			if minLab == 0 || lab < minLab {
				minLab = lab
			}
		}
		s.label[id] = minLab
	}

	return &affectedResult{
		preComponents:  pre.components,
		postComponents: postComponents,
		boundary:       boundary,
		oldLabels:      pre.oldLabels,
	}
}

// buildLocalEvents 只依据受影响旧/新分量构造事件，与全量 buildEvents 等价。
func (s *Service) buildLocalEvents(res *affectedResult) []Event {
	groupByMin := func(comps []map[int64]bool) clusterSnapshot {
		m := make(clusterSnapshot)
		for _, comp := range comps {
			minID := int64(0)
			for id := range comp {
				if minID == 0 || id < minID {
					minID = id
				}
			}
			set := m[minID]
			if set == nil {
				set = make(map[int64]bool)
				m[minID] = set
			}
			for id := range comp {
				set[id] = true
			}
		}
		return m
	}
	return buildEvents(groupByMin(res.preComponents), groupByMin(res.postComponents))
}

// buildEvents 只依据前后两个簇快照构造事件（与增量路径无关）。
func buildEvents(before, after clusterSnapshot) []Event {
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if _, ok := parent[x]; !ok {
			parent[x] = x
		}
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	allNodes := make(map[string]bool)
	for lab := range before {
		allNodes["o:"+strconv.FormatInt(lab, 10)] = true
	}
	for lab := range after {
		allNodes["n:"+strconv.FormatInt(lab, 10)] = true
	}
	for lab, set := range before {
		for id := range set {
			for nlab, nset := range after {
				if nset[id] {
					union("o:"+strconv.FormatInt(lab, 10), "n:"+strconv.FormatInt(nlab, 10))
				}
			}
		}
	}

	groups := make(map[string]map[string]bool)
	for node := range allNodes {
		root := find(node)
		g := groups[root]
		if g == nil {
			g = make(map[string]bool)
			groups[root] = g
		}
		g[node] = true
	}

	var events []Event
	for _, g := range groups {
		var olds, news []int64
		for node := range g {
			lab, _ := strconv.ParseInt(node[2:], 10, 64)
			if node[0] == 'o' {
				olds = append(olds, lab)
			} else {
				news = append(news, lab)
			}
		}
		sort.Slice(olds, func(i, j int) bool { return olds[i] < olds[j] })
		sort.Slice(news, func(i, j int) bool { return news[i] < news[j] })
		switch {
		case len(olds) == 0:
			events = append(events, Event{Type: EventBirth, NewLabels: news})
		case len(news) == 0:
			events = append(events, Event{Type: EventDeath, OldLabels: olds})
		case len(olds) == 1 && len(news) == 1:
			if olds[0] != news[0] {
				events = append(events, Event{Type: EventRelabel, OldLabels: olds, NewLabels: news})
			}
		case len(olds) > 1 && len(news) == 1:
			events = append(events, Event{Type: EventMerge, OldLabels: olds, NewLabels: news})
		case len(olds) == 1 && len(news) > 1:
			events = append(events, Event{Type: EventSplit, OldLabels: olds, NewLabels: news})
		default:
			events = append(events, Event{Type: EventReshape, OldLabels: olds, NewLabels: news})
		}
	}
	sort.Slice(events, func(i, j int) bool {
		return eventMinLabel(events[i]) < eventMinLabel(events[j])
	})
	return events
}

func eventMinLabel(e Event) int64 {
	if len(e.OldLabels) > 0 {
		return e.OldLabels[0]
	}
	return e.NewLabels[0]
}
