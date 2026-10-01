package ontology

func cross(a, b, c Point) int64 {
	return (b.X-a.X)*(c.Y-a.Y) - (b.Y-a.Y)*(c.X-a.X)
}

func coordinateInRange(v int64) bool {
	return -maxCoordinate <= v && v <= maxCoordinate
}

func validateCoordinates(points ...Point) bool {
	for _, point := range points {
		if !coordinateInRange(point.X) || !coordinateInRange(point.Y) {
			return false
		}
	}
	return true
}

func isStrictlyConvex(points []Point) bool {
	if len(points) < 3 {
		return false
	}

	var expectedSign int
	for i, start := range points {
		end := points[(i+1)%len(points)]
		var edgeSign int

		for j := 2; j < len(points); j++ {
			point := points[(i+j)%len(points)]
			value := cross(start, end, point)
			if value == 0 {
				return false
			}

			sign := 1
			if value < 0 {
				sign = -1
			}
			if edgeSign == 0 {
				edgeSign = sign
			} else if edgeSign != sign {
				return false
			}
		}

		if expectedSign == 0 {
			expectedSign = edgeSign
		} else if expectedSign != edgeSign {
			return false
		}
	}

	return true
}

func pointOnOrInsideConvex(p Point, polygon []Point) bool {
	var expectedSign int
	for i, start := range polygon {
		end := polygon[(i+1)%len(polygon)]
		value := cross(start, end, p)
		if value == 0 {
			continue
		}

		sign := 1
		if value < 0 {
			sign = -1
		}
		if expectedSign == 0 {
			expectedSign = sign
		} else if expectedSign != sign {
			return false
		}
	}
	return true
}

func pointStrictInsideConvex(p Point, polygon []Point) bool {
	var expectedSign int
	for i, start := range polygon {
		end := polygon[(i+1)%len(polygon)]
		value := cross(start, end, p)
		if value == 0 {
			return false
		}

		sign := 1
		if value < 0 {
			sign = -1
		}
		if expectedSign == 0 {
			expectedSign = sign
		} else if expectedSign != sign {
			return false
		}
	}
	return true
}

func pointInOpenConvex(p Point, polygon []Point) bool {
	return pointStrictInsideConvex(p, polygon)
}
