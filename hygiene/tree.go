package hygiene

func cloneNode(n *node) *node {
	if n == nil {
		return nil
	}
	copyNode := *n
	if len(n.items) > 0 {
		copyNode.items = make([]*node, len(n.items))
		for i, child := range n.items {
			copyNode.items[i] = cloneNode(child)
		}
	}
	return &copyNode
}
