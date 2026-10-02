package rtree

const coordLimit int64 = 1_000_000_000

func validRect(rect Rect) bool {
	return rect.X1 >= -coordLimit && rect.X1 <= coordLimit &&
		rect.Y1 >= -coordLimit && rect.Y1 <= coordLimit &&
		rect.X2 >= -coordLimit && rect.X2 <= coordLimit &&
		rect.Y2 >= -coordLimit && rect.Y2 <= coordLimit &&
		rect.X1 <= rect.X2 && rect.Y1 <= rect.Y2
}

func unionRect(a, b Rect) Rect {
	return Rect{
		X1: min(a.X1, b.X1),
		Y1: min(a.Y1, b.Y1),
		X2: max(a.X2, b.X2),
		Y2: max(a.Y2, b.Y2),
	}
}

func nodeBounds(entries []entry) Rect {
	if len(entries) == 0 {
		return Rect{}
	}
	bounds := entries[0].rect
	for _, item := range entries[1:] {
		bounds = unionRect(bounds, item.rect)
	}
	return bounds
}

func area(rect Rect) int64 {
	return (rect.X2 - rect.X1) * (rect.Y2 - rect.Y1)
}

func rectsIntersect(a, b Rect) bool {
	return a.X1 <= b.X2 && a.X2 >= b.X1 && a.Y1 <= b.Y2 && a.Y2 >= b.Y1
}

func rectContains(outer, inner Rect) bool {
	return outer.X1 <= inner.X1 && outer.Y1 <= inner.Y1 &&
		outer.X2 >= inner.X2 && outer.Y2 >= inner.Y2
}
