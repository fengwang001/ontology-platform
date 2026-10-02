package ontology

import "sort"

func (m *IncrementalBridges) betterBridge(a, b bridgeValue) bridgeValue {
	if b.empty || (!a.empty && (a.weight < b.weight || (a.weight == b.weight && a.id < b.id))) {
		return a
	}
	return b
}

func (m *IncrementalBridges) pull(x int) {
	if x == 0 {
		return
	}
	left := m.lctChild[x][0]
	right := m.lctChild[x][1]
	m.lctMin[x] = m.betterBridge(m.betterBridge(m.lctMin[left], m.valueAt(x)), m.lctMin[right])
	m.lctFirst[x] = 0
	if m.lctFirst[left] != 0 {
		m.lctFirst[x] = m.lctFirst[left]
	} else if m.lctEdgeID[x] != 0 {
		m.lctFirst[x] = x
	} else if m.lctFirst[right] != 0 {
		m.lctFirst[x] = m.lctFirst[right]
	}
}

func (m *IncrementalBridges) valueAt(x int) bridgeValue {
	if x == 0 || m.lctEdgeID[x] == 0 {
		return m.lctMin[0]
	}
	return bridgeValue{weight: m.lctWeight[x], id: m.lctEdgeID[x]}
}

func (m *IncrementalBridges) applyReverse(x int) {
	if x == 0 {
		return
	}
	m.lctChild[x][0], m.lctChild[x][1] = m.lctChild[x][1], m.lctChild[x][0]
	m.lctRev[x] = !m.lctRev[x]
}

func (m *IncrementalBridges) push(x int) {
	if x != 0 && m.lctRev[x] {
		m.applyReverse(m.lctChild[x][0])
		m.applyReverse(m.lctChild[x][1])
		m.lctRev[x] = false
	}
}

func (m *IncrementalBridges) isRoot(x int) bool {
	parent := m.lctParent[x]
	return parent == 0 || (m.lctChild[parent][0] != x && m.lctChild[parent][1] != x)
}

func (m *IncrementalBridges) rotate(x int) {
	parent := m.lctParent[x]
	grandparent := m.lctParent[parent]
	side := 1
	if m.lctChild[parent][0] == x {
		side = 0
	}
	child := m.lctChild[x][side^1]
	m.lctChild[parent][side] = child
	if child != 0 {
		m.lctParent[child] = parent
	}
	m.lctChild[x][side^1] = parent
	if grandparent != 0 {
		if m.lctChild[grandparent][0] == parent {
			m.lctChild[grandparent][0] = x
		} else if m.lctChild[grandparent][1] == parent {
			m.lctChild[grandparent][1] = x
		}
	}
	m.lctParent[parent] = x
	m.lctParent[x] = grandparent
	m.pull(parent)
	m.pull(x)
}

func (m *IncrementalBridges) splay(x int) {
	stack := make([]int, 0, 32)
	for y := x; ; y = m.lctParent[y] {
		stack = append(stack, y)
		if m.isRoot(y) {
			break
		}
	}
	for i := len(stack) - 1; i >= 0; i-- {
		m.push(stack[i])
	}
	for !m.isRoot(x) {
		parent := m.lctParent[x]
		grandparent := m.lctParent[parent]
		if !m.isRoot(parent) {
			if (m.lctChild[parent][0] == x) == (m.lctChild[grandparent][0] == parent) {
				m.rotate(parent)
			} else {
				m.rotate(x)
			}
		}
		m.rotate(x)
	}
}

func (m *IncrementalBridges) access(x int) {
	last := 0
	for y := x; y != 0; {
		m.splay(y)
		m.lctChild[y][1] = last
		m.pull(y)
		last = y
		y = m.lctParent[y]
	}
	m.splay(x)
}

func (m *IncrementalBridges) evert(x int) {
	m.access(x)
	m.applyReverse(x)
}

func (m *IncrementalBridges) link(a, b int) {
	m.evert(a)
	m.lctParent[a] = b
}

func (m *IncrementalBridges) cut(a, b int) {
	m.evert(a)
	m.access(b)
	x := m.lctChild[b][0]
	m.push(x)
	for m.lctChild[x][1] != 0 {
		x = m.lctChild[x][1]
		m.push(x)
	}
	m.splay(x)
	m.lctChild[x][1] = 0
	m.lctParent[b] = 0
	m.pull(x)
}

func (m *IncrementalBridges) linkBridge(u, v, id int, weight int64) {
	node := m.edgeNode(id)
	m.lctEdgeID[node] = id
	m.lctWeight[node] = weight
	m.lctMin[node] = bridgeValue{weight: weight, id: id}
	m.lctFirst[node] = node
	un := m.vertexNode(u)
	vn := m.vertexNode(v)
	m.link(un, node)
	m.evert(vn)
	m.lctParent[node] = vn
}

func (m *IncrementalBridges) pathBridges(u, v int) []int {
	un := m.vertexNode(u)
	vn := m.vertexNode(v)
	m.evert(un)
	m.access(vn)
	ids := make([]int, 0)
	stack := []int{vn}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if x == 0 || m.lctFirst[x] == 0 {
			continue
		}
		if m.lctEdgeID[x] != 0 {
			ids = append(ids, m.lctEdgeID[x])
		}
		if m.lctFirst[m.lctChild[x][1]] != 0 {
			stack = append(stack, m.lctChild[x][1])
		}
		if m.lctFirst[m.lctChild[x][0]] != 0 {
			stack = append(stack, m.lctChild[x][0])
		}
	}
	return ids
}

func (m *IncrementalBridges) mergeBlocks(u, v int) ([]int, []int) {
	bu := m.findBlock(u)
	bv := m.findBlock(v)
	labelSet := map[int]struct{}{m.label[bu]: {}, m.label[bv]: {}}
	un := m.vertexNode(u)
	vn := m.vertexNode(v)
	m.evert(un)
	m.access(vn)

	type pathEdge struct {
		id int
		a  int
		b  int
	}
	edges := make([]pathEdge, 0)
	edgeIDs := make([]int, 0)
	stack := []int{vn}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if x == 0 || m.lctFirst[x] == 0 {
			continue
		}
		if m.lctEdgeID[x] != 0 {
			id := m.lctEdgeID[x]
			rec := m.edges[id]
			aNode := m.vertexNode(rec.u)
			bNode := m.vertexNode(rec.v)
			edges = append(edges, pathEdge{id: id, a: aNode, b: bNode})
			edgeIDs = append(edgeIDs, id)
			labelSet[m.label[m.findBlock(rec.u)]] = struct{}{}
			labelSet[m.label[m.findBlock(rec.v)]] = struct{}{}
		}
		if m.lctFirst[m.lctChild[x][1]] != 0 {
			stack = append(stack, m.lctChild[x][1])
		}
		if m.lctFirst[m.lctChild[x][0]] != 0 {
			stack = append(stack, m.lctChild[x][0])
		}
	}

	labels := make([]int, 0, len(labelSet))
	for label := range labelSet {
		labels = append(labels, label)
	}
	sort.Ints(labels)
	sort.Ints(edgeIDs)

	for _, edge := range edges {
		m.cut(edge.a, m.edgeNode(edge.id))
		m.cut(m.edgeNode(edge.id), edge.b)
	}
	for _, edge := range edges {
		virtual := m.newLCTNode(0, 0)
		m.link(edge.a, virtual)
		m.evert(edge.b)
		m.lctParent[virtual] = edge.b
	}

	target := labels[0]
	for _, label := range labels[1:] {
		m.blockParent[label] = target
	}
	m.label[target] = target
	m.steps += int64(len(labels))
	return labels, edgeIDs
}
