package regions

// 朴素实现：小坐标下直接用 int 逐边叉积符号判断，作为测试对照。

func naiveCrossSign(ax, ay, bx, by, cx, cy int) int {
	z := (bx-ax)*(cy-by) - (by-ay)*(cx-bx)
	if z > 0 {
		return 1
	}
	if z < 0 {
		return -1
	}
	return 0
}

func ringOrientation(ring Ring) int {
	n := len(ring)
	return naiveCrossSign(
		int(ring[n-1].X), int(ring[n-1].Y),
		int(ring[0].X), int(ring[0].Y),
		int(ring[1].X), int(ring[1].Y),
	)
}

// naiveRingClosed：点在环的闭区域内（边、顶点算内）。
func naiveRingClosed(ring Ring, x, y int) bool {
	n := len(ring)
	sign := ringOrientation(ring)
	for i := 0; i < n; i++ {
		a := ring[i]
		b := ring[(i+1)%n]
		s := naiveCrossSign(int(a.X), int(a.Y), int(b.X), int(b.Y), x, y)
		if s*sign < 0 {
			return false
		}
	}
	return true
}

// naiveRingOpen：点严格在环内部（边上/顶点上为 false）。
func naiveRingOpen(ring Ring, x, y int) bool {
	n := len(ring)
	sign := ringOrientation(ring)
	for i := 0; i < n; i++ {
		a := ring[i]
		b := ring[(i+1)%n]
		s := naiveCrossSign(int(a.X), int(a.Y), int(b.X), int(b.Y), x, y)
		if s*sign <= 0 {
			return false
		}
	}
	return true
}

func naiveContains(r Region, x, y int) bool {
	if !naiveRingClosed(r.Outer, x, y) {
		return false
	}
	if len(r.Hole) > 0 && naiveRingOpen(r.Hole, x, y) {
		return false
	}
	return true
}
