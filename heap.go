package ontology

type elementHeap struct {
	elements  []Element
	direction Direction
	position  map[string]int
}

func newElementHeap(direction Direction) *elementHeap {
	return &elementHeap{direction: direction, position: make(map[string]int)}
}

func (h *elementHeap) Len() int {
	return len(h.elements)
}

func (h *elementHeap) Less(i, j int) bool {
	return weakerInHeap(h.elements[i], h.elements[j], h.direction)
}

func (h *elementHeap) Swap(i, j int) {
	h.elements[i], h.elements[j] = h.elements[j], h.elements[i]
	h.position[h.elements[i].ID] = i
	h.position[h.elements[j].ID] = j
}

func (h *elementHeap) Push(value any) {
	entry := value.(Element)
	h.position[entry.ID] = len(h.elements)
	h.elements = append(h.elements, entry)
}

func (h *elementHeap) Pop() any {
	last := len(h.elements) - 1
	value := h.elements[last]
	h.elements = h.elements[:last]
	delete(h.position, value.ID)
	return value
}

func (h *elementHeap) peek() Element {
	return h.elements[0]
}
