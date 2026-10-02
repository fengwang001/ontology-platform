package rtree

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func unionRect(a, b Rect) Rect {
	return Rect{
		X1: min64(a.X1, b.X1),
		Y1: min64(a.Y1, b.Y1),
		X2: max64(a.X2, b.X2),
		Y2: max64(a.Y2, b.Y2),
	}
}

func rectArea(r Rect) int64 {
	return (r.X2 - r.X1) * (r.Y2 - r.Y1)
}

// intersects reports whether the two closed rectangles share a point,
// including edge and corner touching.
func intersects(a, b Rect) bool {
	return a.X1 <= b.X2 && a.X2 >= b.X1 &&
		a.Y1 <= b.Y2 && a.Y2 >= b.Y1
}

// contains reports whether outer contains inner (boundary included).
func contains(outer, inner Rect) bool {
	return outer.X1 <= inner.X1 && inner.X2 <= outer.X2 &&
		outer.Y1 <= inner.Y1 && inner.Y2 <= outer.Y2
}

func (n *node) mbr() Rect {
	r := n.entries[0].rect
	for i := 1; i < len(n.entries); i++ {
		r = unionRect(r, n.entries[i].rect)
	}
	return r
}

// tightMBR recomputes the tight bounding rectangle of a node.
func (n *node) tightMBR() Rect {
	return n.mbr()
}
