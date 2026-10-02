package rtree

type entry struct {
	id     int64
	rect   Rect
	child  *node
	height int
}

type node struct {
	rect    Rect
	entries []entry
	height  int
}
