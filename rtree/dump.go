package rtree

import "strconv"

func (t *RTree) Dump() string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var builder []byte
	dumpNode(t.root, &builder)
	return string(builder)
}

func dumpNode(n *node, builder *[]byte) {
	if n.height == 0 {
		*builder = append(*builder, 'L')
		dumpRect(n.rect, len(n.entries) > 0, builder)
		*builder = append(*builder, '(')
		for index, item := range n.entries {
			if index > 0 {
				*builder = append(*builder, ' ')
			}
			*builder = strconv.AppendInt(*builder, item.id, 10)
		}
		*builder = append(*builder, ')')
		return
	}

	*builder = append(*builder, 'N')
	dumpRect(n.rect, true, builder)
	*builder = append(*builder, '{')
	for index, item := range n.entries {
		if index > 0 {
			*builder = append(*builder, ',')
		}
		dumpNode(item.child, builder)
	}
	*builder = append(*builder, '}')
}

func dumpRect(rect Rect, include bool, builder *[]byte) {
	if !include {
		*builder = append(*builder, '[', ']')
		return
	}
	*builder = append(*builder, '[')
	for index, value := range []int64{rect.X1, rect.Y1, rect.X2, rect.Y2} {
		if index > 0 {
			*builder = append(*builder, ' ')
		}
		*builder = strconv.AppendInt(*builder, value, 10)
	}
	*builder = append(*builder, ']')
}
