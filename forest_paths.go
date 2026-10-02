package ontology

func edgeGreater(weightA int64, idA int, weightB int64, idB int) bool {
	return weightA > weightB || (weightA == weightB && idA > idB)
}

func (m *DynamicMSF) forestConnected(u, v int) bool {
	return m.component[u] == m.component[v]
}

func (m *DynamicMSF) forestPathMax(u, v int) (int, int64) {
	parentNode := make([]int, m.n)
	parentEdge := make([]int, m.n)
	seen := make([]bool, m.n)
	for i := range parentNode {
		parentNode[i] = -1
		parentEdge[i] = -1
	}
	seen[u] = true
	stack := []int{u}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if node == v {
			break
		}
		for id := range m.forestAdj[node] {
			ed := m.edges[id]
			other := ed.u
			if other == node {
				other = ed.v
			}
			if !seen[other] {
				seen[other] = true
				parentNode[other] = node
				parentEdge[other] = id
				stack = append(stack, other)
			}
		}
	}

	bestID := 0
	var bestWeight int64
	for node := v; node != u; node = parentNode[node] {
		id := parentEdge[node]
		weight := m.edges[id].weight
		if bestID == 0 || edgeGreater(weight, id, bestWeight, bestID) {
			bestID = id
			bestWeight = weight
		}
	}
	return bestID, bestWeight
}

func (m *DynamicMSF) forestLink(id int) {
	ed := m.edges[id]
	m.forestAdj[ed.u][id] = struct{}{}
	m.forestAdj[ed.v][id] = struct{}{}
	m.mergeComponents(ed.u, ed.v)
}

func (m *DynamicMSF) forestCut(id int) {
	ed := m.edges[id]
	delete(m.forestAdj[ed.u], id)
	delete(m.forestAdj[ed.v], id)
	m.splitComponents(ed.u, ed.v)
}

func (m *DynamicMSF) mergeComponents(u, v int) {
	labelA, labelB := m.component[u], m.component[v]
	if labelA == labelB {
		return
	}
	sizeA, sizeB := m.componentSize[labelA], m.componentSize[labelB]
	if sizeA < sizeB {
		labelA, labelB = labelB, labelA
	}
	seed := v
	if m.component[u] == labelB {
		seed = u
	}
	m.sideStamp++
	mark := m.sideStamp
	m.sideMark[seed] = mark
	queue := []int{seed}
	nodes := []int{seed}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		for id := range m.forestAdj[node] {
			ed := m.edges[id]
			other := ed.u
			if other == node {
				other = ed.v
			}
			if m.component[other] == labelB && m.sideMark[other] != mark {
				m.sideMark[other] = mark
				queue = append(queue, other)
				nodes = append(nodes, other)
			}
		}
	}
	for _, node := range nodes {
		m.component[node] = labelA
	}
	m.componentSize[labelA] = sizeA + sizeB
	delete(m.componentSize, labelB)
}

func (m *DynamicMSF) splitComponents(u, v int) {
	oldLabel := m.component[u]
	total := m.componentSize[oldLabel]
	m.sideStamp++
	positive, negative := m.sideStamp, -m.sideStamp
	m.sideMark[u], m.sideMark[v] = positive, negative
	queueA, queueB := []int{u}, []int{v}
	nodesA, nodesB := []int{u}, []int{v}
	side := 1
	expand := func(node, color int, queue, nodes *[]int) {
		for id := range m.forestAdj[node] {
			ed := m.edges[id]
			other := ed.u
			if other == node {
				other = ed.v
			}
			if m.sideMark[other] == positive || m.sideMark[other] == negative {
				continue
			}
			m.sideMark[other] = color
			*queue = append(*queue, other)
			*nodes = append(*nodes, other)
		}
	}
	for len(queueA) > 0 && len(queueB) > 0 {
		if side == 1 {
			node := queueA[0]
			queueA = queueA[1:]
			expand(node, positive, &queueA, &nodesA)
		} else {
			node := queueB[0]
			queueB = queueB[1:]
			expand(node, negative, &queueB, &nodesB)
		}
		side = 3 - side
	}
	var smallNodes []int
	if len(queueA) == 0 {
		smallNodes = nodesA
	} else {
		smallNodes = nodesB
	}
	if len(queueA) == 0 && len(queueB) == 0 && len(nodesB) < len(nodesA) {
		smallNodes = nodesB
	}
	newLabel := m.nextComponent
	m.nextComponent++
	for _, node := range smallNodes {
		m.component[node] = newLabel
	}
	smallCount := len(smallNodes)
	m.componentSize[oldLabel] = total - smallCount
	m.componentSize[newLabel] = smallCount
}
