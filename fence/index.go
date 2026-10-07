package fence

// gridDim is the number of grid cells per axis used by spatialIndex.
const gridDim = 64

// spatialIndex is a uniform grid over the bounding box of all registered
// fences. Each fence is posted to every cell its bounding box covers, so a
// point query only ray-casts against the fences of a single cell instead of
// scanning all fences. Because same-type fences are pairwise disjoint and
// inner fences nest only inside operating fences, at most two fences can
// contain any point; with reasonably distributed fences each cell holds O(1)
// candidates, making point location effectively independent of the fence
// count (verified by TestPointQueryScalesSublinearly).
type spatialIndex struct {
	hasBBox bool
	minX    int64
	minY    int64
	maxX    int64
	maxY    int64
	cellW   int64
	cellH   int64
	cells   map[int64][]string
}

func cellKey(cx, cy int64) int64 { return cx<<32 | (cy & 0xffffffff) }

// buildIndex rebuilds the grid from scratch. Registration is rare compared
// to queries, so an O(total edges) rebuild keeps queries simple and fast.
func buildIndex(fences map[string]*Fence) *spatialIndex {
	idx := &spatialIndex{cells: make(map[int64][]string)}
	first := true
	for _, f := range fences {
		if first {
			idx.minX, idx.minY, idx.maxX, idx.maxY = f.minX, f.minY, f.maxX, f.maxY
			first = false
			continue
		}
		idx.minX = min(idx.minX, f.minX)
		idx.minY = min(idx.minY, f.minY)
		idx.maxX = max(idx.maxX, f.maxX)
		idx.maxY = max(idx.maxY, f.maxY)
	}
	idx.hasBBox = !first
	if !idx.hasBBox {
		return idx
	}
	idx.cellW = max(1, (idx.maxX-idx.minX+1+gridDim-1)/gridDim)
	idx.cellH = max(1, (idx.maxY-idx.minY+1+gridDim-1)/gridDim)
	for id, f := range fences {
		cx0 := (f.minX - idx.minX) / idx.cellW
		cx1 := (f.maxX - idx.minX) / idx.cellW
		cy0 := (f.minY - idx.minY) / idx.cellH
		cy1 := (f.maxY - idx.minY) / idx.cellH
		for cx := cx0; cx <= cx1; cx++ {
			for cy := cy0; cy <= cy1; cy++ {
				key := cellKey(cx, cy)
				idx.cells[key] = append(idx.cells[key], id)
			}
		}
	}
	return idx
}

// candidates returns the IDs of fences whose bounding box covers the cell
// containing p, or nil when p lies outside every fence's bounding box.
func (idx *spatialIndex) candidates(p Point) []string {
	if idx == nil || !idx.hasBBox {
		return nil
	}
	if p.X < idx.minX || p.X > idx.maxX || p.Y < idx.minY || p.Y > idx.maxY {
		return nil
	}
	cx := (p.X - idx.minX) / idx.cellW
	cy := (p.Y - idx.minY) / idx.cellH
	return idx.cells[cellKey(cx, cy)]
}
