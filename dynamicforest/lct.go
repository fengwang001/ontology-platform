package dynamicforest

type linkCutForest struct {
	left        []int
	right       []int
	parent      []int
	reverse     []bool
	size        []int
	virtualSize []int
	edgeID      []int
	weight      []int64
	maxID       []int
	maxWeight   []int64
}

func newLinkCutForest(nodeCount int, edgeLimit int) *linkCutForest {
	capacity := nodeCount + edgeLimit + 1
	forest := &linkCutForest{
		left:        make([]int, capacity),
		right:       make([]int, capacity),
		parent:      make([]int, capacity),
		reverse:     make([]bool, capacity),
		size:        make([]int, capacity),
		virtualSize: make([]int, capacity),
		edgeID:      make([]int, capacity),
		weight:      make([]int64, capacity),
		maxID:       make([]int, capacity),
		maxWeight:   make([]int64, capacity),
	}
	for node := 1; node <= nodeCount; node++ {
		forest.size[node] = 1
	}
	return forest
}

func vertexNode(node int) int {
	return node + 1
}

func edgeNode(edgeID int, nodeCount int) int {
	return nodeCount + edgeID
}

func (f *linkCutForest) ensureEdgeCapacity(edgeID int, nodeCount int) {
	required := nodeCount + edgeID + 1
	if required <= len(f.left) {
		return
	}
	growth := required
	if doubled := len(f.left) * 2; doubled > growth {
		growth = doubled
	}
	f.left = append(f.left, make([]int, growth-len(f.left))...)
	f.right = append(f.right, make([]int, growth-len(f.right))...)
	f.parent = append(f.parent, make([]int, growth-len(f.parent))...)
	f.reverse = append(f.reverse, make([]bool, growth-len(f.reverse))...)
	f.size = append(f.size, make([]int, growth-len(f.size))...)
	f.virtualSize = append(f.virtualSize, make([]int, growth-len(f.virtualSize))...)
	f.edgeID = append(f.edgeID, make([]int, growth-len(f.edgeID))...)
	f.weight = append(f.weight, make([]int64, growth-len(f.weight))...)
	f.maxID = append(f.maxID, make([]int, growth-len(f.maxID))...)
	f.maxWeight = append(f.maxWeight, make([]int64, growth-len(f.maxWeight))...)
}

func greaterEdge(firstID int, firstWeight int64, secondID int, secondWeight int64) bool {
	return firstWeight > secondWeight || (firstWeight == secondWeight && firstID > secondID)
}

func smallerKey(id int, weight int64, bestID int, bestWeight int64) bool {
	return weight < bestWeight || (weight == bestWeight && id < bestID)
}

func (f *linkCutForest) isAuxiliaryRoot(node int) bool {
	parent := f.parent[node]
	return parent == 0 || (f.left[parent] != node && f.right[parent] != node)
}

func (f *linkCutForest) applyReverse(node int) {
	if node == 0 {
		return
	}
	f.left[node], f.right[node] = f.right[node], f.left[node]
	f.reverse[node] = !f.reverse[node]
}

func (f *linkCutForest) push(node int) {
	if node == 0 || !f.reverse[node] {
		return
	}
	f.applyReverse(f.left[node])
	f.applyReverse(f.right[node])
	f.reverse[node] = false
}

func (f *linkCutForest) pull(node int) {
	if node == 0 {
		return
	}
	left := f.left[node]
	right := f.right[node]
	f.size[node] = 1 + f.size[left] + f.size[right] + f.virtualSize[node]

	bestID := f.edgeID[node]
	bestWeight := f.weight[node]
	if left != 0 && f.maxID[left] != 0 && (bestID == 0 || greaterEdge(f.maxID[left], f.maxWeight[left], bestID, bestWeight)) {
		bestID = f.maxID[left]
		bestWeight = f.maxWeight[left]
	}
	if right != 0 && f.maxID[right] != 0 && (bestID == 0 || greaterEdge(f.maxID[right], f.maxWeight[right], bestID, bestWeight)) {
		bestID = f.maxID[right]
		bestWeight = f.maxWeight[right]
	}
	f.maxID[node] = bestID
	f.maxWeight[node] = bestWeight
}

func (f *linkCutForest) rotate(node int) {
	parent := f.parent[node]
	grandparent := f.parent[parent]
	if f.left[parent] == node {
		child := f.right[node]
		f.right[node] = parent
		f.left[parent] = child
		if child != 0 {
			f.parent[child] = parent
		}
	} else {
		child := f.left[node]
		f.left[node] = parent
		f.right[parent] = child
		if child != 0 {
			f.parent[child] = parent
		}
	}
	f.parent[node] = grandparent
	f.parent[parent] = node
	if grandparent != 0 {
		if f.left[grandparent] == parent {
			f.left[grandparent] = node
		} else if f.right[grandparent] == parent {
			f.right[grandparent] = node
		}
	}
	f.pull(parent)
	f.pull(node)
}

func (f *linkCutForest) splay(node int) {
	path := []int{node}
	for current := node; !f.isAuxiliaryRoot(current); current = f.parent[current] {
		path = append(path, f.parent[current])
	}
	for index := len(path) - 1; index >= 0; index-- {
		f.push(path[index])
	}

	for !f.isAuxiliaryRoot(node) {
		parent := f.parent[node]
		grandparent := f.parent[parent]
		if !f.isAuxiliaryRoot(parent) {
			if (f.left[parent] == node) == (f.left[grandparent] == parent) {
				f.rotate(parent)
			} else {
				f.rotate(node)
			}
		}
		f.rotate(node)
	}
}

func (f *linkCutForest) access(node int) {
	last := 0
	for current := node; current != 0; current = f.parent[current] {
		f.splay(current)
		f.virtualSize[current] += f.size[f.right[current]]
		f.virtualSize[current] -= f.size[last]
		f.right[current] = last
		f.pull(current)
		last = current
	}
	f.splay(node)
}

func (f *linkCutForest) makeRoot(node int) {
	f.access(node)
	f.applyReverse(node)
}

func (f *linkCutForest) findRoot(node int) int {
	f.access(node)
	for f.left[node] != 0 {
		f.push(node)
		node = f.left[node]
	}
	f.access(node)
	return node
}

func (f *linkCutForest) connected(first int, second int) bool {
	f.makeRoot(first)
	return f.findRoot(second) == first
}

func (f *linkCutForest) pathMax(first int, second int) (int, int64) {
	f.makeRoot(first)
	f.access(second)
	return f.maxID[second], f.maxWeight[second]
}

func (f *linkCutForest) componentSize(node int) int {
	f.access(node)
	return f.size[node]
}

func (f *linkCutForest) link(first int, second int) {
	f.makeRoot(first)
	f.access(second)
	f.parent[first] = second
	f.virtualSize[second] += f.size[first]
	f.pull(second)
}

func (f *linkCutForest) cut(first int, second int) {
	f.makeRoot(first)
	f.access(second)
	f.left[second] = 0
	f.parent[first] = 0
	f.pull(second)
}

func (f *linkCutForest) linkEdge(edge *edgeRecord, nodeCount int) {
	edgeVertex := edgeNode(edge.id, nodeCount)
	f.left[edgeVertex] = 0
	f.right[edgeVertex] = 0
	f.parent[edgeVertex] = 0
	f.reverse[edgeVertex] = false
	f.virtualSize[edgeVertex] = 0
	f.edgeID[edgeVertex] = edge.id
	f.weight[edgeVertex] = edge.weight
	f.maxID[edgeVertex] = edge.id
	f.maxWeight[edgeVertex] = edge.weight
	f.size[edgeVertex] = 1

	first := vertexNode(edge.u)
	second := vertexNode(edge.v)
	f.link(first, edgeVertex)
	f.link(edgeVertex, second)
}

func (f *linkCutForest) cutEdge(edge *edgeRecord, nodeCount int) {
	edgeVertex := edgeNode(edge.id, nodeCount)
	first := vertexNode(edge.u)
	second := vertexNode(edge.v)
	f.cut(first, edgeVertex)
	f.cut(edgeVertex, second)
}

func (f *linkCutForest) setEdgeWeight(edge *edgeRecord, nodeCount int) {
	edgeVertex := edgeNode(edge.id, nodeCount)
	f.weight[edgeVertex] = edge.weight
	f.maxWeight[edgeVertex] = edge.weight
	f.access(edgeVertex)
}
