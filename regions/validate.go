package regions

// validateRegion 按规定顺序执行全部几何与坐标检查：
// 坐标越界 -> 顶点数 -> 外环凸性 -> 洞凸性 -> 洞在外环内。
func validateRegion(r Region) error {
	if !coordsInRange(r.Outer) || !coordsInRange(r.Hole) {
		return ErrCoordOutOfRange
	}
	if len(r.Outer) < 3 {
		return ErrTooFewVertices
	}
	if len(r.Hole) > 0 && len(r.Hole) < 3 {
		return ErrTooFewVertices
	}
	if !strictlyConvex(r.Outer) {
		return ErrOuterNotConvex
	}
	if len(r.Hole) > 0 && !strictlyConvex(r.Hole) {
		return ErrHoleNotConvex
	}
	if len(r.Hole) > 0 && !holeStrictlyInside(r.Outer, r.Hole) {
		return ErrHoleNotInside
	}
	return nil
}
